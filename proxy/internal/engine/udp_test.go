package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/leroux/torii-qubes/claims"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"

	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

// udpEcho is a loopback UDP server answering "<tag>:<payload>".
func udpEcho(t *testing.T, tag string) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		b := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			pc.WriteTo([]byte(tag+":"+string(b[:n])), from)
		}
	}()
	return pc.LocalAddr().String()
}

// udpInternet maps fake internet addresses to loopback UDP servers.
type udpInternet struct {
	*fakeInternet
	mu  sync.Mutex
	udp map[string]string
}

func (u *udpInternet) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "udp" {
		return u.fakeInternet.DialContext(ctx, network, addr)
	}
	u.fakeInternet.mu.Lock()
	u.fakeInternet.count["udp:"+addr]++
	u.fakeInternet.mu.Unlock()
	u.mu.Lock()
	real, ok := u.udp[addr]
	u.mu.Unlock()
	if !ok {
		real = "127.0.0.1:9" // discard: nothing answers
	}
	return net.Dial("udp", real)
}

type captureReply struct {
	ch     chan string
	closed chan struct{}
	once   sync.Once
}

func newCapture() *captureReply {
	return &captureReply{ch: make(chan string, 16), closed: make(chan struct{})}
}
func (c *captureReply) Send(b []byte) error { c.ch <- string(b); return nil }
func (c *captureReply) Close() error        { c.once.Do(func() { close(c.closed) }); return nil }

func (c *captureReply) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-c.ch:
		if got != want {
			t.Fatalf("reply %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no reply, want %q", want)
	}
}

func (c *captureReply) expectNone(t *testing.T) {
	t.Helper()
	select {
	case got := <-c.ch:
		t.Fatalf("unexpected reply %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

func udpWorld(t *testing.T) (*world, *udpInternet) {
	w := newWorld(t)
	ui := &udpInternet{fakeInternet: w.internet, udp: map[string]string{}}
	w.eng.d.Internet = ui
	w.eng.d.UDPIdle = 300 * time.Millisecond
	w.net.InternetUDP = func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		return net.Dial("udp", udpEchoFor(t, w, "via-exit"))
	}
	return w, ui
}

var exitEcho sync.Map

func udpEchoFor(t *testing.T, w *world, tag string) string {
	if v, ok := exitEcho.Load(w); ok {
		return v.(string)
	}
	a := udpEcho(t, tag)
	exitEcho.Store(w, a)
	return a
}

func (w *world) udp(src, dst string, payload string, r *captureReply) {
	w.eng.HandleUDP(context.Background(), netip.MustParseAddrPort(src), netip.MustParseAddrPort(dst), []byte(payload),
		func() (UDPReply, error) { return r, nil })
}

func countEvents(l *events.Log, f func(events.Event) bool) int {
	n := 0
	for _, e := range l.Recent("") {
		if f(e) {
			n++
		}
	}
	return n
}

func TestUDPRoutedFlows(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_QQYS, "flow-follows-first", "idle-flow-redecided")
	w, ui := udpWorld(t)
	w.start(t)
	ui.udp["93.184.216.34:443"] = udpEcho(t, "quic")
	w.internet.name("pypi.org", "151.101.0.223")
	ui.udp["151.101.0.223:443"] = udpEcho(t, "pypi")
	store := w.net.Node("store")
	pc, _ := store.ListenUDP(5353)
	go func() {
		b := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			pc.WriteTo([]byte("store:"+string(b[:n])), from)
		}
	}()
	t.Cleanup(func() { pc.Close() })

	t.Run("full internet, one decision per flow", func(t *testing.T) {
		r := newCapture()
		w.udp("10.137.0.21:40000", "93.184.216.34:443", "a", r)
		r.expect(t, "quic:a")
		w.udp("10.137.0.21:40000", "93.184.216.34:443", "b", r)
		r.expect(t, "quic:b")
		n := countEvents(w.eng.Log(), func(e events.Event) bool { return e.Network == "udp" && e.Dest == "93.184.216.34:443" })
		if n != 1 {
			t.Errorf("%d events for one flow", n)
		}
	})

	t.Run("allowlist by resolved name", func(t *testing.T) {
		browser := netip.MustParseAddr("10.137.0.20")
		r := newCapture()
		w.udp("10.137.0.20:40001", "151.101.0.223:443", "x", r)
		r.expectNone(t) // not resolved yet
		ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Network == "udp" && e.Reason == policy.ReasonUnresolvedIP })
		if ev.Workload != "browser" {
			t.Errorf("%+v", ev)
		}
		w.eng.AnswerDNS(context.Background(), browser, dnsQuery(t, "pypi.org", dnsmessage.TypeA))
		r2 := newCapture()
		w.udp("10.137.0.20:40002", "151.101.0.223:443", "y", r2)
		r2.expect(t, "pypi:y")
	})

	t.Run("tailnet through the identity", func(t *testing.T) {
		r := newCapture()
		w.udp("10.137.0.20:40003", netip.AddrPortFrom(store.IP(), 5353).String(), "q", r)
		r.expect(t, "store:q")
		// web has no tailnet outbound.
		r2 := newCapture()
		w.udp("10.137.0.21:40003", netip.AddrPortFrom(store.IP(), 5353).String(), "q", r2)
		r2.expectNone(t)
	})

	t.Run("unknown source and forbidden addresses", func(t *testing.T) {
		r := newCapture()
		w.udp("10.137.0.99:1", "93.184.216.34:443", "x", r)
		w.udp("10.137.0.21:1", "127.0.0.1:53", "x", r)
		w.udp("10.137.0.21:1", "100.64.0.200:41641", "x", r)
		r.expectNone(t)
		waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Network == "udp" && e.Reason == policy.ReasonUnknownWorkload })
		waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Network == "udp" && e.Reason == policy.ReasonForbiddenAddress })
	})

	t.Run("an idle flow ends and closes its reply", func(t *testing.T) {
		r := newCapture()
		w.udp("10.137.0.21:40009", "93.184.216.34:443", "a", r)
		r.expect(t, "quic:a")
		select {
		case <-r.closed:
		case <-time.After(3 * time.Second):
			t.Fatal("flow never ended")
		}
		waitFor(t, func() bool { return w.eng.Status(context.Background()).Workloads["web"].Active == 0 })
		// The next datagram is a new flow, decided again.
		before := countEvents(w.eng.Log(), func(e events.Event) bool { return e.Source == "web" && e.Network == "udp" })
		r2 := newCapture()
		w.udp("10.137.0.21:40009", "93.184.216.34:443", "b", r2)
		r2.expect(t, "quic:b")
		if after := countEvents(w.eng.Log(), func(e events.Event) bool { return e.Source == "web" && e.Network == "udp" }); after != before+1 {
			t.Errorf("events %d -> %d", before, after)
		}
	})
}

// A refused first datagram settles its flow: later datagrams of the flow
// are dropped without a new decision, even once the name is resolved,
// until the flow goes idle.
func TestUDPRefusedFlowStaysRefused(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_QQYS, "flow-follows-first")
	w, ui := udpWorld(t)
	w.eng.d.UDPIdle = time.Minute
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	ui.udp["151.101.0.223:443"] = udpEcho(t, "pypi")
	r := newCapture()
	w.udp("10.137.0.20:41000", "151.101.0.223:443", "x", r)
	r.expectNone(t) // not resolved yet: refused
	w.eng.AnswerDNS(context.Background(), netip.MustParseAddr("10.137.0.20"), dnsQuery(t, "pypi.org", dnsmessage.TypeA))
	before := countEvents(w.eng.Log(), func(e events.Event) bool { return e.Network == "udp" && e.Source == "browser" })
	r2 := newCapture()
	w.udp("10.137.0.20:41000", "151.101.0.223:443", "y", r2)
	r2.expectNone(t) // same flow: still refused
	if after := countEvents(w.eng.Log(), func(e events.Event) bool { return e.Network == "udp" && e.Source == "browser" }); after != before {
		t.Errorf("a refused flow was decided again: events %d -> %d", before, after)
	}
	r3 := newCapture()
	w.udp("10.137.0.20:41001", "151.101.0.223:443", "z", r3)
	r3.expect(t, "pypi:z") // a new flow is decided, and now allowed
}

func TestUDPExitNodeFailsClosed(t *testing.T) {
	w, _ := udpWorld(t)
	exit := w.net.Node("exit-1")
	exit.OfferExitNode()
	w.start(t)
	waitFor(t, func() bool { return w.net.Node("egress-vpn").Status(context.Background()).ExitNode == "exit-1" })
	r := newCapture()
	w.udp("10.137.0.22:40000", "8.8.8.8:443", "a", r)
	r.expect(t, "via-exit:a")
	exit.SetOffline(true)
	r2 := newCapture()
	w.udp("10.137.0.22:40001", "8.8.8.8:443", "b", r2)
	r2.expectNone(t)
	if n := w.internet.dials("udp:8.8.8.8:443"); n != 0 {
		t.Errorf("fell back to the local network %d times", n)
	}
}

func TestProxyQubeOwnOutbound(t *testing.T) {
	cfg, err := config.Parse([]byte(`{"proxy_qube":"sys-torii","state_dir":"/s","listen":{"local_socks":"127.0.0.1:0"},
	  "workloads":{
	    "sys-torii": {"link":"none","identity":{"tags":["tag:proxy"]},"tailnet_outbound":true,"socks":true,
	                "internet":"allowlist","internet_allow":["pypi.org"],
	                "forwards":[{"name":"db","to":"store:8000","listen":` + fmt.Sprint(freePort(t)) + `}]},
	    "job": {"link":"qrexec","socks":true,"internet":"full"},
	    "web": {"link":"routed","address":"10.137.0.21","internet":"full"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	fn := &tailnet.FakeNet{}
	inet := newFakeInternet(t)
	inet.name("pypi.org", "151.101.0.223")
	inet.serve(t, "151.101.0.223:443", "pypi")
	eng := New(Deps{Config: cfg, Identities: map[string]tailnet.Identity{"sys-torii": fn.Node("sys-torii")},
		Internet: inet, Resolver: inet, Log: &events.Log{}, SNIWait: 100 * time.Millisecond})
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	serveFake(t, fn.Node("store"), 8000, "store")
	addrs := eng.LocalAddrs()

	d, _ := proxy.SOCKS5("tcp", addrs["socks"], nil, proxy.Direct)
	for target, want := range map[string]string{"pypi.org:443": "pypi", "store:8000": "store"} {
		c, err := d.Dial("tcp", target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		expectGreeting(t, c, want)
		c.Close()
	}
	if _, err := d.Dial("tcp", "evil.com:443"); err == nil {
		t.Error("evil.com allowed")
	}
	c, err := net.Dial("tcp", addrs["f.db"])
	if err != nil {
		t.Fatal(err)
	}
	expectGreeting(t, c, "store")
	c.Close()
	if !strings.HasPrefix(addrs["socks"], "127.0.0.1:") {
		t.Errorf("local socks on %s", addrs["socks"])
	}

	// qrexec calls come only from qrexec workloads, whatever dom0 names.
	for _, caller := range []string{"sys-torii", "web", "nobody"} {
		a, b := net.Pipe()
		go eng.HandleQrexec(context.Background(), caller, "socks", b)
		a.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.ReadAll(a); err != nil && !errors.Is(err, io.EOF) {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				t.Errorf("%s: call left open", caller)
			}
		}
	}
	for _, r := range []policy.Reason{policy.ReasonWrongEntry, policy.ReasonUnknownWorkload} {
		waitEvent(t, eng.Log(), func(e events.Event) bool { return e.Reason == r })
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestProxyQubeOwnAddressesForbidden(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_WNYB, "proxy-self-forbidden")
	w, ui := udpWorld(t)
	w.eng.d.SelfAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.100.2")} }
	w.start(t)
	w.internet.serve(t, "192.168.100.2:41641", "proxy-own-socket")
	ui.udp["192.168.100.2:41641"] = udpEcho(t, "proxy-own-socket")
	w.internet.name("proxy.lan", "192.168.100.2")
	w.internet.name("mixed.example", "192.168.100.2", "93.184.216.34")
	w.internet.serve(t, "93.184.216.34:41641", "public")

	// SOCKS to the address, and to a name that resolves to it.
	w.cfg.Workloads["job"].Internet = config.InternetFull
	w.cfg.Workloads["job"].InternetAllow = nil
	if _, err := w.socksDial("job", "192.168.100.2:41641"); err == nil {
		t.Error("reached the proxy's own address")
	}
	if _, err := w.socksDial("job", "proxy.lan:41641"); err == nil {
		t.Error("reached the proxy through a name")
	}
	c, err := w.socksDial("job", "mixed.example:41641") // the public address still works
	if err != nil {
		t.Fatal(err)
	}
	expectGreeting(t, c, "public")
	c.Close()
	// Intercepted TCP and UDP from a routed qube.
	a, b := net.Pipe()
	go w.eng.HandleIntercepted(context.Background(), b, netip.MustParseAddrPort("10.137.0.21:1"), netip.MustParseAddrPort("192.168.100.2:41641"))
	expectClosed(t, a)
	r := newCapture()
	w.udp("10.137.0.21:2", "192.168.100.2:41641", "x", r)
	r.expectNone(t)
	if n := w.internet.dials("192.168.100.2:41641") + w.internet.dials("udp:192.168.100.2:41641"); n != 0 {
		t.Errorf("dialled the proxy's own address %d times", n)
	}
	// DNS never hands it out.
	_, addrs := dnsAnswer(t, w.eng.AnswerDNS(context.Background(), netip.MustParseAddr("10.137.0.21"), dnsQuery(t, "proxy.lan", dnsmessage.TypeA)))
	if len(addrs) != 0 {
		t.Errorf("dns answered %v", addrs)
	}
}

func TestUDPFlowLimit(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_QQYS, "cap-refuses")
	w, ui := udpWorld(t)
	w.eng.d.MaxUDPFlows = 3
	w.start(t)
	ui.udp["93.184.216.34:443"] = udpEcho(t, "quic")
	var replies []*captureReply
	for i := 0; i < 6; i++ {
		r := newCapture()
		replies = append(replies, r)
		w.udp(fmt.Sprintf("10.137.0.21:%d", 50000+i), "93.184.216.34:443", "x", r)
	}
	for i, r := range replies {
		if i < 3 {
			r.expect(t, "quic:x")
		} else {
			r.expectNone(t)
		}
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Reason == policy.ReasonUDPFlowLimit && e.Workload == "web" })
	// Another workload is unaffected.
	r := newCapture()
	w.udp("10.137.0.22:1", "93.184.216.34:443", "x", r) // work: exit node not offered, so refused, but decided
	waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "work" && e.Network == "udp" })
	// Once flows end, there is room again.
	waitFor(t, func() bool { return w.eng.Status(context.Background()).Workloads["web"].Active == 0 })
	r2 := newCapture()
	w.udp("10.137.0.21:60000", "93.184.216.34:443", "y", r2)
	r2.expect(t, "quic:y")
}
