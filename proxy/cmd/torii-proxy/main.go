// Command torii-proxy is the proxy program of a Qubes proxy qube.
//
//	torii-proxy run -config FILE|qubesdb:KEY [-install-rules] [-downstream 'vif*']
//	torii-proxy connect-service      # body of /etc/qubes-rpc/torii.Connect
//	torii-proxy status-service       # body of /etc/qubes-rpc/torii.Status (dom0, read-only)
//	torii-proxy status|log|explain   # local queries over the admin socket
//	torii-proxy rules                # print the nftables rules
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/leroux/torii-qubes/proxy/internal/admin"
	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/daemon"
	"github.com/leroux/torii-qubes/proxy/internal/intercept"
	"github.com/leroux/torii-qubes/proxy/internal/qrexec"
	"github.com/leroux/torii-qubes/proxy/internal/qubesdb"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run":
		err = run(args)
	case "connect-service":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		sock := fs.String("socket", config.DefaultConnect, "connect socket")
		fs.Parse(args)
		err = qrexec.ExecService(*sock, os.Getenv, os.Stdin, os.Stdout)
	case "status-service":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		sock := fs.String("socket", config.DefaultAdmin, "admin socket")
		fs.Parse(args)
		err = statusService(admin.Client{Socket: *sock})
	case "status", "log", "explain":
		err = query(cmd, args)
	case "rules":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		down := fs.String("downstream", "vif*", "downstream interface pattern")
		fs.Parse(args)
		fmt.Print(intercept.Rules(*down, 15001, 15053, 15002))
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "torii-proxy:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: torii-proxy run|connect-service|status-service|status|log|explain|rules [flags]")
	os.Exit(2)
}

// loadConfig reads a file, or the per-qube features dom0 set
// ("qubesdb:torii": /vm-config/torii and its parts, see internal/qubesdb).
func loadConfig(src string) (*config.Config, error) {
	if key, ok := strings.CutPrefix(src, "qubesdb:"); ok {
		raw, err := qubesdb.Config(qubesdb.Read, key)
		if err != nil {
			return nil, err
		}
		return config.Parse(raw)
	}
	return config.Load(src)
}

// run waits for a valid config, runs the daemon on it, and replaces it
// only with the next valid one.
//
//charter:realizes SW-261127-XGDY, SW-261127-8PTP
func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	src := fs.String("config", "qubesdb:torii", "config file, or qubesdb:KEY")
	install := fs.Bool("install-rules", false, "apply nftables rules and turn forwarding off")
	down := fs.String("downstream", "vif*", "interface pattern routed workloads arrive on")
	verbose := fs.Bool("v", false, "log tsnet")
	fs.Parse(args)
	opt := daemon.Options{InstallRules: *install, Downstream: *down}
	if *verbose {
		opt.TSLogf = log.Printf
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wait for a valid config: a new proxy qube may start before apply
	// has written one, and a half-written one is refused.
	var cfg *config.Config
	for {
		var err error
		if cfg, err = loadConfig(*src); err == nil {
			break
		}
		log.Printf("waiting for a config: %v", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
	changed := make(chan struct{}, 1)
	if strings.HasPrefix(*src, "qubesdb:") {
		go func() {
			for ctx.Err() == nil {
				if err := qubesdb.Watch(ctx, func() {
					select {
					case changed <- struct{}{}:
					default:
					}
				}); err != nil && ctx.Err() == nil {
					log.Printf("qubesdb-watch: %v", err)
				}
				time.Sleep(time.Second)
			}
		}()
	}
	for {
		if tailnet.SetTelemetry(cfg.TailscaleLogs) {
			// Log uploads were turned off in this process for good; the
			// service manager starts it again with them on.
			log.Printf("config %s turns Tailscale log uploads on: restarting", cfg.Version)
			return nil
		}
		d, err := daemon.Start(ctx, cfg, opt)
		if err != nil {
			return err
		}
		log.Printf("torii-proxy %s: %d workloads, config %s", cfg.ProxyQube, len(cfg.Workloads), cfg.Version)
		next := waitForNewConfig(ctx, *src, cfg.Version, changed)
		// Identities keep their state on disk, so a restart reconnects
		// them without new sign-ins; open connections end.
		if err := d.Close(); err != nil {
			log.Printf("stopping: %v", err)
		}
		if next == nil {
			return nil
		}
		log.Printf("config %s replaces %s", next.Version, cfg.Version)
		cfg = next
	}
}

// waitForNewConfig returns the next valid config with another version,
// or nil when ctx ends. Invalid configs (a write in progress, a bad one)
// leave the running config in place.
//
//charter:realizes SW-261127-XGDY
func waitForNewConfig(ctx context.Context, src, version string, changed <-chan struct{}) *config.Config {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		}
		// Let dom0 finish writing every part.
		time.Sleep(500 * time.Millisecond)
		c, err := loadConfig(src)
		if err != nil {
			log.Printf("config change not taken: %v", err)
			continue
		}
		if c.Version != version {
			return c
		}
	}
}

// statusService prints status and recent decisions as one JSON object.
//
//charter:realizes SW-261127-B8BM
func statusService(c admin.Client) error {
	ctx := context.Background()
	var st, lg json.RawMessage
	if err := c.Get(ctx, "/status", nil, &st); err != nil {
		return err
	}
	if err := c.Get(ctx, "/log", nil, &lg); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]json.RawMessage{"status": st, "log": lg})
}

func query(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	sock := fs.String("socket", config.DefaultAdmin, "admin socket")
	w := fs.String("workload", "", "workload")
	dest := fs.String("dest", "", "explain: host:port (out) or port (in)")
	dir := fs.String("direction", "out", "explain: out or in")
	sni := fs.String("sni", "", "explain: TLS server name")
	fs.Parse(args)
	q := url.Values{}
	if *w != "" {
		q.Set("workload", *w)
	}
	if cmd == "explain" {
		q.Set("dest", *dest)
		q.Set("direction", *dir)
		q.Set("sni", *sni)
	}
	return admin.Client{Socket: *sock}.Get(context.Background(), "/"+cmd, q, os.Stdout)
}
