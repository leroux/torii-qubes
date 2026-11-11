// Command lintel-client runs in a workload without a network card. It
// listens on loopback ports and turns each connection into one qrexec
// call, so programs in the workload reach the outside through its proxy
// qube, as dom0 allows:
//
//	127.0.0.1:1080 -> lintel.Connect+socks      (SOCKS5)
//	127.0.0.1:3128 -> lintel.Connect+http       (HTTPS_PROXY / HTTP_PROXY)
//	127.0.0.1:5432 -> lintel.Connect+f.db       (a fixed forward)
//	127.0.0.1:22   -> qubes.ConnectTCP+22 to another qube (local-inbound)
//
// Which ports, from the per-qube feature vm-config.lintel-client that the
// dom0 tool sets (JSON: {"listen": [{"port", "service", "target"}]}). It
// exits when that feature changes; systemd starts it again. Enabled per
// qube by dom0 with the qvm-service lintel-client.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"

	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/qubesdb"
)

type listen struct {
	Port    uint16 `json:"port"`
	Service string `json:"service"`
	Target  string `json:"target"`
}

type clientConfig struct {
	Listen []listen `json:"listen"`
}

var (
	serviceRe = regexp.MustCompile(`^(lintel\.Connect\+(socks|http|f\.[A-Za-z0-9_-]{1,40})|qubes\.ConnectTCP\+[0-9]{1,5})$`)
	targetRe  = regexp.MustCompile(`^(@default|[A-Za-z][A-Za-z0-9_.-]{0,30})$`)
)

func parse(b []byte) (*clientConfig, error) {
	var c clientConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	seen := map[uint16]bool{}
	for _, l := range c.Listen {
		if l.Port == 0 || seen[l.Port] {
			return nil, fmt.Errorf("port %d: zero or twice", l.Port)
		}
		seen[l.Port] = true
		if !serviceRe.MatchString(l.Service) || !targetRe.MatchString(l.Target) {
			return nil, fmt.Errorf("port %d: bad service %q or target %q", l.Port, l.Service, l.Target)
		}
	}
	return &c, nil
}

func main() {
	src := flag.String("config", "qubesdb:/vm-config/lintel-client", "config: a file, or qubesdb:PATH")
	addr := flag.String("addr", "127.0.0.1", "address to listen on")
	qrexec := flag.String("qrexec-client-vm", "qrexec-client-vm", "qrexec client (tests)")
	flag.Parse()

	var raw []byte
	var err error
	if p, ok := cutPrefix(*src, "qubesdb:"); ok {
		raw, err = qubesdb.Read(p)
	} else {
		raw, err = os.ReadFile(*src)
	}
	if err != nil {
		log.Fatalf("lintel-client: %v", err)
	}
	cfg, err := parse(raw)
	if err != nil {
		log.Fatalf("lintel-client: config: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for _, l := range cfg.Listen {
		ln, err := net.Listen("tcp", net.JoinHostPort(*addr, fmt.Sprint(l.Port)))
		if err != nil {
			log.Fatalf("lintel-client: %v", err)
		}
		log.Printf("127.0.0.1:%d -> %s %s", l.Port, l.Target, l.Service)
		go serve(ctx, ln, l, *qrexec)
	}
	if _, ok := cutPrefix(*src, "qubesdb:"); ok {
		go func() {
			// The first event is the watch starting; any later one is a change.
			first := true
			_ = qubesdb.Watch(ctx, func() {
				if first {
					first = false
					return
				}
				log.Printf("config changed; restarting")
				stop()
			})
		}()
	}
	<-ctx.Done()
}

func serve(ctx context.Context, ln net.Listener, l listen, qrexec string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			cmd := exec.CommandContext(ctx, qrexec, l.Target, l.Service)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = c, c, os.Stderr
			if err := cmd.Run(); err != nil && ctx.Err() == nil {
				log.Printf("%s %s: %v", l.Target, l.Service, err)
			}
		}()
	}
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return s, false
}
