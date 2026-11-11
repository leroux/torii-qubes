// Package itest runs the proxy program against a real tailnet in one
// process: tsnet nodes, real WireGuard, a local DERP relay and
// Tailscale's in-process test coordination server (testcontrol).
package itest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/proxy"
	"tailscale.com/ipn"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"

	"github.com/Gaia-PBC/torii-qubes/proxy/internal/config"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/engine"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/events"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/policy"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/tailnet"
)

func startControl(t *testing.T, requireAuth bool) (string, *testcontrol.Server) {
	t.Helper()
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	derpMap := integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1")
	control := &testcontrol.Server{
		DERPMap:        derpMap,
		DNSConfig:      &tailcfg.DNSConfig{Proxied: true},
		MagicDNSDomain: "tail.test",
		RequireAuth:    requireAuth,
		Logf:           logger.Discard,
	}
	control.HTTPTestServer = httptest.NewUnstartedServer(control)
	control.HTTPTestServer.Start()
	t.Cleanup(control.HTTPTestServer.Close)
	return control.HTTPTestServer.URL, control
}

func newID(t *testing.T, controlURL, host string) *tailnet.TSNet {
	return tailnet.NewTSNet(tailnet.TSNetOptions{
		Hostname: host, Dir: filepath.Join(t.TempDir(), host), ControlURL: controlURL,
		Store: new(mem.Store), Ephemeral: true,
	})
}

func up(t *testing.T, ctx context.Context, id *tailnet.TSNet) {
	t.Helper()
	if err := id.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := id.AwaitRunning(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { id.Close() })
}

// localIP is an address of this machine the proxy treats as the internet,
// standing in for it. A Qubes qube has none (10.137/16 is Qubes' own and
// never dialled), so there the test is skipped.
func localIP(t *testing.T) netip.Addr {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err == nil && p.Addr().Is4() && policy.ZoneOf(p.Addr()) == policy.ZoneInternet {
			return p.Addr()
		}
	}
	t.Skip("no local IPv4 address the proxy would treat as the internet (on a Qubes qube, 10.137/16 is Qubes' own)")
	return netip.Addr{}
}

func serveGreeting(t *testing.T, ln net.Listener, greeting string) {
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				fmt.Fprintf(c, "%s\n", greeting)
				io.Copy(c, c)
			}()
		}
	}()
}

func readLine(t *testing.T, c net.Conn) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(20 * time.Second))
	s, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSpace(s)
}

type qubes struct {
	mu sync.Mutex
	m  map[string]string
}

func (q *qubes) DialQube(ctx context.Context, qube string, port uint16) (net.Conn, error) {
	q.mu.Lock()
	a := q.m[fmt.Sprintf("%s:%d", qube, port)]
	q.mu.Unlock()
	var d net.Dialer
	return d.DialContext(ctx, "tcp", a)
}

func TestRealTailnet(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a tailnet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	controlURL, control := startControl(t, false)

	// The service inside the airgapped qube, reached over "qrexec".
	svc, _ := net.Listen("tcp", "127.0.0.1:0")
	serveGreeting(t, svc, "infra")
	q := &qubes{m: map[string]string{"infra:443": svc.Addr().String()}}

	// "The internet": a server on this machine's own address.
	inetIP := localIP(t)
	inet, err := net.Listen("tcp", netip.AddrPortFrom(inetIP, 0).String())
	if err != nil {
		t.Fatal(err)
	}
	serveGreeting(t, inet, "internet")
	inetAddr := netip.MustParseAddrPort(inet.Addr().String())

	// Other machines on the tailnet.
	laptop := newID(t, controlURL, "laptop")
	up(t, ctx, laptop)
	store := newID(t, controlURL, "store")
	up(t, ctx, store)
	rln, _ := store.Listen(8000)
	serveGreeting(t, rln, "store")
	exit := tailnet.NewTSNet(tailnet.TSNetOptions{
		Hostname: "exit-1", Dir: filepath.Join(t.TempDir(), "exit-1"), ControlURL: controlURL,
		Store: new(mem.Store), Ephemeral: true, Logf: debugLogf(t),
	})
	up(t, ctx, exit)
	lc, _ := exit.Server().LocalClient()
	exitRoutes := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	if _, err := lc.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{AdvertiseRoutes: exitRoutes}, AdvertiseRoutesSet: true}); err != nil {
		t.Fatal(err)
	}
	est, err := lc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	control.SetSubnetRoutes(est.Self.PublicKey, exitRoutes)
	// A tsnet node does not forward what it advertises by itself (it
	// resets connections nothing listens for); a real exit node is
	// tailscaled. This makes the test's exit node dial out.
	exit.Server().RegisterFallbackTCPHandler(func(src, dst netip.AddrPort) (func(net.Conn), bool) {
		return func(c net.Conn) {
			defer c.Close()
			up, err := net.Dial("tcp", dst.String())
			if err != nil {
				return
			}
			defer up.Close()
			go io.Copy(up, c)
			io.Copy(c, up)
		}, true
	})

	cfg := &config.Config{
		Version: "it", ProxyQube: "sys-torii", StateDir: t.TempDir(),
		Workloads: map[string]*config.Workload{
			"infra": {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:server"}},
				Inbound: []config.Inbound{{Port: 443}}},
			"job":     {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:job"}}, TailnetOutbound: true, SOCKS: true},
			"browser": {Link: config.LinkRouted, Address: "10.137.0.23", Identity: &config.Identity{Tags: []string{"tag:browser"}}, TailnetOutbound: true},
			"work": {Link: config.LinkRouted, Address: "10.137.0.22", Identity: &config.Identity{Login: true},
				Internet: config.InternetFull, Egress: "vpn"},
		},
		Egresses: map[string]*config.Egress{"vpn": {Hostname: "egress-vpn", Tags: []string{"tag:egress"}, Exits: []string{"exit-1"}}},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	ids := map[string]tailnet.Identity{}
	for _, n := range []string{"infra", "job", "work", "browser"} {
		ids[n] = newID(t, controlURL, n)
	}
	ids[config.EgressIdentityKey("vpn")] = newID(t, controlURL, "egress-vpn")
	direct := &countingDialer{}
	log := &events.Log{}
	eng := engine.New(engine.Deps{Config: cfg, Identities: ids, Qubes: q, Internet: direct, Log: log, DialTimeout: 5 * time.Second})
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	for n, id := range ids {
		if err := id.AwaitRunning(ctx); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}

	t.Run("inbound to the airgapped service", func(t *testing.T) {
		st := ids["infra"].Status(ctx)
		ip := netip.MustParseAddr(st.IPs[0])
		var c net.Conn
		var err error
		for i := 0; i < 50; i++ { // the netmap reaches the laptop
			if c, err = laptop.Dial(ctx, netip.AddrPortFrom(ip, 443)); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if got := readLine(t, c); got != "infra" {
			t.Fatalf("got %q", got)
		}
		ev := waitEvent(t, log, func(e events.Event) bool { return e.Direction == events.In && e.Workload == "infra" })
		if !strings.HasPrefix(ev.Source, "laptop.") {
			t.Errorf("inbound source names the tailnet peer: %+v", ev)
		}
		// An undeclared port does not answer.
		dctx, dc := context.WithTimeout(ctx, 2*time.Second)
		defer dc()
		if c2, err := laptop.Dial(dctx, netip.AddrPortFrom(ip, 22)); err == nil {
			c2.Close()
			t.Error("port 22 answered")
		}
	})

	t.Run("udp from a routed workload over wireguard", func(t *testing.T) {
		pc, err := store.ListenUDP(5353)
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		go func() {
			b := make([]byte, 1500)
			for {
				n, from, err := pc.ReadFrom(b)
				if err != nil {
					return
				}
				pc.WriteTo(append([]byte("store:"), b[:n]...), from)
			}
		}()
		got := make(chan string, 4)
		dst := netip.AddrPortFrom(netip.MustParseAddr(store.Status(ctx).IPs[0]), 5353)
		send := func(src string) {
			eng.HandleUDP(ctx, netip.MustParseAddrPort(src), dst, []byte("hi"), func() (engine.UDPReply, error) {
				return replyFunc(func(b []byte) { got <- string(b) }), nil
			})
		}
		for i := 0; i < 20; i++ {
			send(fmt.Sprintf("10.137.0.23:%d", 40000+i)) // new flow each try while netmaps settle
			select {
			case r := <-got:
				if r != "store:hi" {
					t.Fatalf("got %q", r)
				}
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		t.Fatal("no udp reply over the tailnet")
	})

	t.Run("socks to a tailnet name", func(t *testing.T) {
		d, _ := proxy.SOCKS5("tcp", "qrexec", nil, pipeDialer(func() net.Conn {
			a, b := net.Pipe()
			go eng.HandleQrexec(ctx, "job", "socks", b)
			return a
		}))
		var c net.Conn
		var err error
		for i := 0; i < 50; i++ {
			if c, err = d.(proxy.ContextDialer).DialContext(ctx, "tcp", "store.tail.test:8000"); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if got := readLine(t, c); got != "store" {
			t.Fatalf("got %q", got)
		}
		// job has no internet: refused before any dial.
		if _, err := d.(proxy.ContextDialer).DialContext(ctx, "tcp", inetAddr.String()); err == nil {
			t.Error("job reached the internet")
		}
	})

	t.Run("an identity never dials the system network", func(t *testing.T) {
		// job's identity has no exit node: an internet address has no route.
		dctx, dc := context.WithTimeout(ctx, 2*time.Second)
		defer dc()
		if c, err := ids["job"].Dial(dctx, inetAddr); err == nil {
			c.Close()
			t.Fatal("identity reached the internet without an exit node")
		}
		if ids["job"].IsTailnet(ctx, inetIP) {
			t.Error("internet address classified as tailnet")
		}
		if !ids["job"].IsTailnet(ctx, netip.MustParseAddr(store.Status(ctx).IPs[0])) {
			t.Error("peer address not classified as tailnet")
		}
	})

	t.Run("exit node carries internet and fails closed", func(t *testing.T) {
		// The egress's identity carries it (pinged healthy first), not work's own.
		waitFor(t, 30*time.Second, func() bool { return eng.Status(ctx).Egresses["vpn"].Usable })
		if ids["work"].Status(ctx).ExitNode != "" {
			t.Error("the exit node was set on the workload's own identity")
		}
		src := netip.MustParseAddrPort("10.137.0.22:40000")
		var got string
		for i := 0; i < 30 && got == ""; i++ {
			a, b := net.Pipe()
			go eng.HandleIntercepted(ctx, b, src, inetAddr)
			a.SetReadDeadline(time.Now().Add(3 * time.Second))
			if s, err := bufio.NewReader(a).ReadString('\n'); err == nil {
				got = strings.TrimSpace(s)
			}
			a.Close()
		}
		if got != "internet" {
			for _, e := range log.Recent("work") {
				t.Logf("%+v", e)
			}
			t.Fatalf("through the exit node: %q", got)
		}
		if direct.n() != 0 {
			t.Fatal("exit-node workload used the proxy qube's own network")
		}
		exit.Close() // the exit node goes away
		a, b := net.Pipe()
		go eng.HandleIntercepted(ctx, b, src, inetAddr)
		a.SetReadDeadline(time.Now().Add(15 * time.Second))
		if s, err := bufio.NewReader(a).ReadString('\n'); err == nil {
			t.Fatalf("got %q with the exit node gone", s)
		}
		a.Close()
		if direct.n() != 0 {
			t.Fatal("fell back to the local network")
		}
	})
}

func TestSignInByLink(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a tailnet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	controlURL, control := startControl(t, true)
	cfg := &config.Config{ProxyQube: "p", StateDir: t.TempDir(), Workloads: map[string]*config.Workload{
		"svc": {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:server"}}, Inbound: []config.Inbound{{Port: 80}}},
	}}
	cfg.Normalize()
	id := newID(t, controlURL, "svc")
	eng := engine.New(engine.Deps{Config: cfg, Identities: map[string]tailnet.Identity{"svc": id}})
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	var url string
	waitFor(t, 20*time.Second, func() bool {
		url = eng.Status(ctx).Workloads["svc"].Identity.AuthURL
		return url != ""
	})
	if st := eng.Status(ctx).Workloads["svc"].Identity.State; st != "NeedsLogin" {
		t.Errorf("state %s", st)
	}
	if !control.CompleteAuth(url) { // the admin opens the link and approves
		t.Fatalf("control did not know %s", url)
	}
	if err := id.AwaitRunning(ctx); err != nil {
		t.Fatal(err)
	}
	if u := eng.Status(ctx).Workloads["svc"].Identity.AuthURL; u != "" {
		t.Errorf("link still shown after sign-in: %s", u)
	}
}

type countingDialer struct {
	mu    sync.Mutex
	count int
}

func (d *countingDialer) n() int { d.mu.Lock(); defer d.mu.Unlock(); return d.count }
func (d *countingDialer) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	d.mu.Lock()
	d.count++
	d.mu.Unlock()
	var nd net.Dialer
	return nd.DialContext(ctx, n, a)
}

type pipeDialer func() net.Conn

func (p pipeDialer) Dial(string, string) (net.Conn, error) { return p(), nil }
func (p pipeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return p(), nil
}

func waitEvent(t *testing.T, l *events.Log, f func(events.Event) bool) events.Event {
	t.Helper()
	var out events.Event
	waitFor(t, 10*time.Second, func() bool {
		for _, e := range l.Recent("") {
			if f(e) {
				out = e
				return true
			}
		}
		return false
	})
	return out
}

func waitFor(t *testing.T, d time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func debugLogf(t *testing.T) logger.Logf {
	if os.Getenv("TORII_DEBUG") == "" {
		return logger.Discard
	}
	return func(f string, a ...any) { t.Logf("exit: "+f, a...) }
}

type replyFunc func([]byte)

func (f replyFunc) Send(b []byte) error { f(b); return nil }
func (f replyFunc) Close() error        { return nil }
