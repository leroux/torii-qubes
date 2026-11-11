// Package daemon runs the proxy program: identities, engine, and every
// listener, from one config.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/admin"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/config"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/engine"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/events"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/intercept"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/qrexec"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/tailnet"
)

// Options adjust a run; the zero value is production.
type Options struct {
	// Identities, when set, replaces the tsnet identities (tests).
	Identities map[string]tailnet.Identity
	// InstallRules applies the nftables rules for routed workloads.
	InstallRules bool
	// Downstream is the interface pattern routed workloads arrive on.
	Downstream string
	// Qubes, Internet, Resolver override the engine's dependencies.
	Qubes    engine.QubeDialer
	Internet engine.Dialer
	Resolver engine.Resolver
	Local    engine.Dialer
	// TSLogf receives tsnet's own logs; nil discards them.
	TSLogf func(string, ...any)
	Logf   func(string, ...any)
}

// Daemon is a running program.
type Daemon struct {
	Engine *engine.Engine
	cfg    *config.Config
	lns    []closer
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

type closer interface{ Close() error }

// Identities builds the tsnet identities of cfg.
func Identities(cfg *config.Config, logf func(string, ...any)) (map[string]tailnet.Identity, error) {
	ids := map[string]tailnet.Identity{}
	for _, name := range cfg.WorkloadNames() {
		w := cfg.Workloads[name]
		if w.Identity == nil {
			continue
		}
		key := ""
		if w.Identity.AuthKeyFile != "" {
			// No key file: sign in by link. A key is used once; tsnet keeps
			// the identity's state after that.
			b, err := os.ReadFile(w.Identity.AuthKeyFile)
			if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("auth key for %s: %w", name, err)
			}
			key = strings.TrimSpace(string(b))
		}
		ids[name] = tailnet.NewTSNet(tailnet.TSNetOptions{
			Hostname:   w.Identity.Hostname,
			Dir:        filepath.Join(cfg.StateDir, name),
			ControlURL: cfg.ControlURL,
			Tags:       w.Identity.Tags,
			AuthKey:    key,
			Ephemeral:  w.Identity.Ephemeral,
			Logf:       logf,
		})
	}
	return ids, nil
}

// Start brings the program up.
func Start(ctx context.Context, cfg *config.Config, opt Options) (*Daemon, error) {
	if opt.Logf == nil {
		opt.Logf = log.Printf
	}
	if opt.Downstream == "" {
		opt.Downstream = "vif*"
	}
	ids := opt.Identities
	if ids == nil {
		var err error
		if ids, err = Identities(cfg, opt.TSLogf); err != nil {
			return nil, err
		}
	}
	if cfg.LogDir != "" {
		if err := os.MkdirAll(cfg.LogDir, 0o750); err != nil {
			return nil, err
		}
	}
	qubes := opt.Qubes
	if qubes == nil {
		qubes = &qrexec.Dialer{}
	}
	eng := engine.New(engine.Deps{
		Config: cfg, Identities: ids, Qubes: qubes, Internet: opt.Internet, Resolver: opt.Resolver, Local: opt.Local,
		Log: &events.Log{Dir: cfg.LogDir}, Logf: opt.Logf,
	})
	ctx, cancel := context.WithCancel(ctx)
	d := &Daemon{Engine: eng, cfg: cfg, cancel: cancel}
	fail := func(err error) (*Daemon, error) { d.Close(); return nil, err }

	var routed, viaQrexec bool
	for _, w := range cfg.Workloads {
		routed = routed || w.Link == config.LinkRouted
		viaQrexec = viaQrexec || w.Link == config.LinkQrexec
	}
	adm, err := qrexec.ListenUnix(cfg.Listen.Admin, 0o600)
	if err != nil {
		return fail(err)
	}
	d.add(adm)
	srv := &http.Server{Handler: admin.Handler(eng)}
	d.add(srv)
	d.go_(func() { srv.Serve(adm) })

	if viaQrexec {
		// qrexec-agent (root) and the exec helper (the qube's user) connect.
		ln, err := qrexec.ListenUnix(cfg.Listen.Connect, 0o666)
		if err != nil {
			return fail(err)
		}
		d.add(ln)
		d.go_(func() { qrexec.Serve(ctx, ln, eng.HandleQrexec) })
	}
	if routed {
		tln, err := net.Listen("tcp", cfg.Listen.Intercept)
		if err != nil {
			return fail(err)
		}
		d.add(tln)
		d.go_(func() { intercept.ServeTCP(ctx, tln, eng) })
		pc, err := net.ListenPacket("udp", cfg.Listen.DNS)
		if err != nil {
			return fail(err)
		}
		d.add(pc)
		d.go_(func() { intercept.ServeDNSUDP(ctx, pc, eng) })
		dln, err := net.Listen("tcp", cfg.Listen.DNS)
		if err != nil {
			return fail(err)
		}
		d.add(dln)
		d.go_(func() { intercept.ServeDNSTCP(ctx, dln, eng) })
		upc, err := intercept.ListenUDP(cfg.Listen.UDP)
		if err != nil {
			return fail(fmt.Errorf("udp (needs CAP_NET_ADMIN): %w", err))
		}
		d.add(upc)
		d.go_(func() { intercept.ServeUDP(ctx, upc, eng) })
		if opt.InstallRules {
			if err := intercept.Install(opt.Downstream, port(cfg.Listen.Intercept), port(cfg.Listen.DNS), port(cfg.Listen.UDP)); err != nil {
				return fail(err)
			}
		}
	}
	if err := eng.Start(ctx); err != nil {
		return fail(err)
	}
	d.go_(func() { d.relaySignIn(ctx) })
	return d, nil
}

// relaySignIn prints each new sign-in link once, for dom0 to relay.
func (d *Daemon) relaySignIn(ctx context.Context) {
	seen := map[string]bool{}
	for {
		for name, w := range d.Engine.Status(ctx).Workloads {
			if w.Identity != nil && w.Identity.AuthURL != "" && !seen[w.Identity.AuthURL] {
				seen[w.Identity.AuthURL] = true
				log.Printf("sign-in for %s (%s): %s", name, w.Identity.Hostname, w.Identity.AuthURL)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (d *Daemon) add(c closer) { d.lns = append(d.lns, c) }
func (d *Daemon) go_(f func()) {
	d.wg.Add(1)
	go func() { defer d.wg.Done(); f() }()
}

// Close stops everything.
func (d *Daemon) Close() error {
	d.cancel()
	for _, c := range d.lns {
		c.Close()
	}
	err := d.Engine.Close()
	d.wg.Wait()
	return err
}

func port(addr string) uint16 {
	_, p, err := config.SplitHostPort(addr)
	if err != nil {
		panic(errors.New("bad listen address " + addr))
	}
	return p
}
