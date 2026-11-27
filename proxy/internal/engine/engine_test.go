package engine

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/leroux/torii-qubes/claims"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"

	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

const testCfg = `{
  "version": "t1", "proxy_qube": "sys-torii", "state_dir": "/unused",
  "workloads": {
    "airgap":  {"link": "qrexec", "identity": {"tags": ["tag:server"]}, "inbound": [{"port": 443, "target": 8443}]},
    "airgap.web": {"link": "qrexec", "qube": "airgap", "identity": {"hostname": "airgap-web", "tags": ["tag:web"]},
                "inbound": [{"port": 443, "target": 3000, "tls": true}]},
    "job":     {"link": "qrexec", "identity": {"tags": ["tag:job"]}, "tailnet_outbound": true, "socks": true, "http": true,
                "internet": "allowlist", "internet_allow": ["pypi.org"],
                "forwards": [{"name": "db", "to": "store:8000"}, {"name": "pypi", "to": "pypi.org:443"}]},
    "job3":    {"link": "qrexec", "identity": {"tags": ["tag:job"]}, "tailnet_outbound": true, "tailnet_allow": ["store"], "socks": true, "http": true},
    "browser": {"link": "routed", "address": "10.137.0.20", "identity": {"tags": ["tag:browser"]}, "tailnet_outbound": true,
                "internet": "allowlist", "internet_allow": ["pypi.org", "pypi.org/udp"]},
    "web":     {"link": "routed", "address": "10.137.0.21", "internet": "full", "identity": {"tags": ["tag:web"]}, "inbound": [{"port": 80}]},
    "work":    {"link": "routed", "address": "10.137.0.22", "identity": {"login": true}, "internet": "full", "egress": "vpn"}
  },
  "egresses": {"vpn": {"hostname": "egress-vpn", "tags": ["tag:egress"], "exits": ["exit-1", "exit-2"]}}}`

// world is everything outside the proxy, faked.
type world struct {
	net      *tailnet.FakeNet
	cfg      *config.Config
	eng      *Engine
	internet *fakeInternet
	qubes    *fakeQubes
	local    *fakeInternet
}

func newWorld(t *testing.T) *world {
	t.Helper()
	cfg, err := config.Parse([]byte(testCfg))
	if err != nil {
		t.Fatal(err)
	}
	w := &world{cfg: cfg, net: &tailnet.FakeNet{}, internet: newFakeInternet(t), qubes: &fakeQubes{t: t, servers: map[string]string{}}, local: newFakeInternet(t)}
	// The exit node's own internet is a separate world: traffic that
	// reaches it never touches the proxy qube's internet dialer.
	exitInternet := newFakeInternet(t)
	exitInternet.serve(t, "93.184.216.34:443", "via-exit")
	w.net.Internet = exitInternet.dial
	ids := map[string]tailnet.Identity{}
	for _, n := range cfg.WorkloadNames() {
		if cfg.Workloads[n].Identity != nil {
			ids[n] = w.net.Node(cfg.Workloads[n].Identity.Hostname)
		}
	}
	for _, n := range cfg.EgressNames() {
		ids[config.EgressIdentityKey(n)] = w.net.Node(cfg.Egresses[n].Hostname)
	}
	w.eng = New(Deps{
		Config: cfg, Identities: ids, Internet: w.internet, Resolver: w.internet, Qubes: w.qubes, Local: w.local,
		Log: &events.Log{}, SNIWait: 200 * time.Millisecond, DialTimeout: 2 * time.Second,
	})
	t.Cleanup(func() { w.eng.Close() })
	return w
}

func (w *world) start(t *testing.T) {
	t.Helper()
	if err := w.eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// waitEvent waits for an event matching f.
func waitEvent(t *testing.T, l *events.Log, f func(events.Event) bool) events.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range l.Recent("") {
			if f(e) {
				return e
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no matching event; have %+v", l.Recent(""))
	return events.Event{}
}

func TestInboundToAirgappedQube(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_T7GG, "listed-port-mapped", "unlisted-inbound-refused")
	w := newWorld(t)
	w.qubes.serve(t, "airgap", 8443, "airgap-service")
	w.start(t)
	laptop := w.net.Node("laptop")
	airgap := w.net.Node("airgap")

	c, err := laptop.Dial(context.Background(), netip.AddrPortFrom(airgap.IP(), 443))
	if err != nil {
		t.Fatal(err)
	}
	expectGreeting(t, c, "airgap-service")
	echo(t, c)
	c.Close()
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Direction == events.In })
	if ev.Verdict != policy.Allow || ev.Source != "laptop.fake.ts.net" || ev.Workload != "airgap" {
		t.Errorf("event %+v", ev)
	}
	// Only declared ports listen.
	if _, err := laptop.Dial(context.Background(), netip.AddrPortFrom(airgap.IP(), 22)); err == nil {
		t.Error("undeclared port reachable")
	}
	// Inbound is one qrexec call per connection, proxy -> workload.
	if got := w.qubes.calls.Load(); got != 1 {
		t.Errorf("qrexec calls %d", got)
	}
}

func TestInboundToRoutedQube(t *testing.T) {
	w := newWorld(t)
	w.qubes.serve(t, "web", 80, "web-site")
	w.start(t)
	c, err := w.net.Node("laptop").Dial(context.Background(), netip.AddrPortFrom(w.net.Node("web").IP(), 80))
	if err != nil {
		t.Fatal(err)
	}
	expectGreeting(t, c, "web-site")
	c.Close()
}

// socksDial dials target through the engine's SOCKS entry as caller.
func (w *world) socksDial(caller, target string) (net.Conn, error) {
	d, _ := proxy.SOCKS5("tcp", "qrexec", nil, dialFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
		a, b := net.Pipe()
		go w.eng.HandleQrexec(context.Background(), caller, "socks", b)
		return a, nil
	}))
	return d.(proxy.ContextDialer).DialContext(context.Background(), "tcp", target)
}

func TestSOCKSTailnetAndAllowlist(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	store := w.net.Node("store")
	serveFake(t, store, 8000, "store")
	w.internet.serve(t, "151.101.0.223:443", "pypi")
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.name("evil.com", "6.6.6.6")
	w.internet.name("rebind.pypi.org", "127.0.0.1")

	c, err := w.socksDial("job", "store:8000")
	if err != nil {
		t.Fatal(err)
	}
	expectGreeting(t, c, "store")
	c.Close()

	c, err = w.socksDial("job", "store.fake.ts.net:8000")
	if err != nil {
		t.Fatal(err)
	}
	expectGreeting(t, c, "store")
	c.Close()

	c, err = w.socksDial("job", "pypi.org:443")
	if err != nil {
		t.Fatal(err)
	}
	expectGreeting(t, c, "pypi")
	c.Close()

	if _, err := w.socksDial("job", "evil.com:443"); err == nil {
		t.Error("evil.com allowed")
	}
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return strings.HasPrefix(e.Dest, "evil.com") })
	if ev.Reason != policy.ReasonNotAllowlisted {
		t.Errorf("%+v", ev)
	}
	if _, err := w.socksDial("job", "151.101.0.223:443"); err == nil {
		t.Error("raw ip allowed on allowlist")
	}
	// Nobody else may use the SOCKS port: airgap has no outbound.
	if _, err := w.socksDial("airgap", "store:8000"); err == nil {
		t.Error("airgap reached the tailnet")
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "airgap" && e.Reason == policy.ReasonNoSOCKS })
	// A routed qube calling qrexec is refused too.
	if _, err := w.socksDial("browser", "store:8000"); err == nil {
		t.Error("routed qube used socks")
	}
	if w.internet.dials("6.6.6.6:443") != 0 {
		t.Error("refused destination was dialled")
	}
}

func TestSOCKSServerNameMustBeAllowed(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_6S2F, "sni-mismatch-refused")
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.serveRaw(t, "151.101.0.223:443", func(c net.Conn) { io.Copy(io.Discard, c) })

	c, err := w.socksDial("job", "pypi.org:443")
	if err != nil {
		t.Fatal(err)
	}
	// Domain fronting: the allowed name to SOCKS, another name to TLS.
	tc := tls.Client(c, &tls.Config{ServerName: "evil.com", InsecureSkipVerify: true})
	tc.SetDeadline(time.Now().Add(2 * time.Second))
	if err := tc.Handshake(); err == nil {
		t.Fatal("handshake went through")
	}
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.SNI == "evil.com" })
	if ev.Reason != policy.ReasonSNINotAllowed || ev.Verdict != policy.Refuse {
		t.Errorf("%+v", ev)
	}
}

// Fronting after a pause: the ClientHello comes later than the wait for
// the first bytes. Waiting must not turn the check off; the server name is
// read whenever the client first speaks. The upstream server sees nothing.
func TestSOCKSServerNameAfterPause(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_6S2F, "sni-mismatch-refused", "pause-does-not-skip")
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	var got atomic.Int64
	w.internet.serveRaw(t, "151.101.0.223:443", func(c net.Conn) { n, _ := io.Copy(io.Discard, c); got.Add(n) })

	c, err := w.socksDial("job", "pypi.org:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(3 * w.eng.d.SNIWait) // the pause is the attack, not a wait for the code
	tc := tls.Client(c, &tls.Config{ServerName: "evil.com", InsecureSkipVerify: true})
	tc.SetDeadline(time.Now().Add(2 * time.Second))
	if err := tc.Handshake(); err == nil {
		t.Fatal("handshake went through")
	}
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "job" && e.Dest == "pypi.org:443" })
	if ev.Reason != policy.ReasonSNINotAllowed || ev.Verdict != policy.Refuse || ev.SNI != "evil.com" {
		t.Errorf("%+v", ev)
	}
	if got.Load() != 0 {
		t.Errorf("upstream got %d bytes", got.Load())
	}
}

// Fronting with the ClientHello split across TLS records: the name is
// still read, and refused.
func TestSOCKSServerNameSplitAcrossRecords(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	got := make(chan int, 1)
	w.internet.serveRaw(t, "151.101.0.223:443", func(c net.Conn) {
		n, _ := io.Copy(io.Discard, c)
		got <- int(n)
	})

	// Go's ClientHello for evil.com, re-sent as records of 64 bytes each.
	a, b := net.Pipe()
	go tls.Client(a, &tls.Config{ServerName: "evil.com", InsecureSkipVerify: true}).Handshake()
	hdr := make([]byte, 5)
	io.ReadFull(b, hdr)
	body := make([]byte, int(hdr[3])<<8|int(hdr[4]))
	io.ReadFull(b, body)
	a.Close()
	var wire []byte
	for len(body) > 0 {
		k := min(64, len(body))
		wire = append(wire, 0x16, 3, 1, 0, byte(k))
		wire = append(wire, body[:k]...)
		body = body[k:]
	}

	c, err := w.socksDial("job", "pypi.org:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(wire)
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.SNI == "evil.com" })
	if ev.Reason != policy.ReasonSNINotAllowed || ev.Verdict != policy.Refuse {
		t.Errorf("%+v", ev)
	}
	select {
	case n := <-got:
		if n > 0 {
			t.Errorf("server got %d bytes", n)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// A connection that opens like TLS but has no readable ClientHello is
// refused, not relayed as if it had sent no name.
func TestSOCKSUnreadableClientHello(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_6S2F, "unreadable-hello-refused")
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.serveRaw(t, "151.101.0.223:443", func(c net.Conn) { io.Copy(io.Discard, c) })

	c, err := w.socksDial("job", "pypi.org:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte{0x16, 3, 1, 0, 8, 1, 0, 0, 4, 0xde, 0xad, 0xbe, 0xef})
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Reason == policy.ReasonSNIUnreadable })
	if ev.Verdict != policy.Refuse || ev.Workload != "job" {
		t.Errorf("%+v", ev)
	}
}

// A client that closes before sending anything, where the server name is
// read before deciding, leaves an event: verdict closed, nothing relayed.
// Without it the connection ended with no evidence at all.
func TestClosedBeforeData(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	var relayed atomic.Bool
	w.internet.serveRaw(t, "151.101.0.223:443", func(c net.Conn) {
		if n, _ := io.Copy(io.Discard, c); n > 0 {
			relayed.Store(true)
		}
	})
	closed := func(entry policy.Entry) func(events.Event) bool {
		return func(e events.Event) bool { return e.Entry == entry && e.Verdict == policy.Closed }
	}

	c, err := w.socksDial("job", "pypi.org:443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	ev := waitEvent(t, w.eng.Log(), closed(policy.EntrySOCKS))
	if ev.Workload != "job" || ev.Reason != policy.ReasonClosedBeforeData || ev.Path != "" {
		t.Errorf("socks: %+v", ev)
	}

	w.eng.AnswerDNS(context.Background(), netip.MustParseAddr("10.137.0.20"), dnsQuery(t, "pypi.org", dnsmessage.TypeA))
	a, b := net.Pipe()
	go w.eng.HandleIntercepted(context.Background(), b, netip.MustParseAddrPort("10.137.0.20:40000"), netip.MustParseAddrPort("151.101.0.223:443"))
	a.Close()
	ev = waitEvent(t, w.eng.Log(), closed(policy.EntryIntercept))
	if ev.Workload != "browser" || ev.Reason != policy.ReasonClosedBeforeData || ev.Dest != "151.101.0.223:443" {
		t.Errorf("intercept: %+v", ev)
	}
	if relayed.Load() {
		t.Error("bytes reached the server")
	}
}

func TestForwards(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	serveFake(t, w.net.Node("store"), 8000, "store")
	a, b := net.Pipe()
	go w.eng.HandleQrexec(context.Background(), "job", "f.db", b)
	expectGreeting(t, a, "store")
	a.Close()

	a, b = net.Pipe()
	done := make(chan struct{})
	go func() { w.eng.HandleQrexec(context.Background(), "job", "f.nope", b); close(done) }()
	<-done
	waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Reason == policy.ReasonUnknownForward })
	a.Close()
}

func dnsQuery(t *testing.T, name string, typ dnsmessage.Type) []byte {
	m := dnsmessage.Message{Header: dnsmessage.Header{ID: 7, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name + "."), Type: typ, Class: dnsmessage.ClassINET}}}
	b, _ := m.Pack()
	return b
}

func dnsAnswer(t *testing.T, b []byte) (dnsmessage.RCode, []netip.Addr) {
	t.Helper()
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		t.Fatal(err)
	}
	var out []netip.Addr
	for _, a := range m.Answers {
		switch r := a.Body.(type) {
		case *dnsmessage.AResource:
			out = append(out, netip.AddrFrom4(r.A))
		case *dnsmessage.AAAAResource:
			out = append(out, netip.AddrFrom16(r.AAAA))
		}
	}
	return m.RCode, out
}

func TestRoutedDNSThenConnect(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_2R9T, "resolved-name-allows", "unresolved-address-refused")
	w := newWorld(t)
	w.start(t)
	browser := netip.MustParseAddr("10.137.0.20")
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.serve(t, "151.101.0.223:443", "pypi")
	w.internet.serve(t, "151.101.0.224:443", "unnamed")
	serveFake(t, w.net.Node("store"), 8000, "store")

	rc, addrs := dnsAnswer(t, w.eng.AnswerDNS(context.Background(), browser, dnsQuery(t, "pypi.org", dnsmessage.TypeA)))
	if rc != dnsmessage.RCodeSuccess || len(addrs) != 1 || addrs[0].String() != "151.101.0.223" {
		t.Fatalf("pypi.org: %v %v", rc, addrs)
	}
	rc, _ = dnsAnswer(t, w.eng.AnswerDNS(context.Background(), browser, dnsQuery(t, "evil.com", dnsmessage.TypeA)))
	if rc != dnsmessage.RCodeNameError {
		t.Errorf("evil.com: %v", rc)
	}
	rc, addrs = dnsAnswer(t, w.eng.AnswerDNS(context.Background(), browser, dnsQuery(t, "store.fake.ts.net", dnsmessage.TypeA)))
	if rc != dnsmessage.RCodeSuccess || len(addrs) != 1 || addrs[0] != w.net.Node("store").IP() {
		t.Errorf("tailnet name: %v %v", rc, addrs)
	}
	rc, _ = dnsAnswer(t, w.eng.AnswerDNS(context.Background(), browser, dnsQuery(t, "gone.fake.ts.net", dnsmessage.TypeA)))
	if rc != dnsmessage.RCodeNameError {
		t.Errorf("unknown tailnet name: %v", rc)
	}
	rc, _ = dnsAnswer(t, w.eng.AnswerDNS(context.Background(), netip.MustParseAddr("10.137.0.99"), dnsQuery(t, "pypi.org", dnsmessage.TypeA)))
	if rc != dnsmessage.RCodeRefused {
		t.Errorf("unknown source: %v", rc)
	}

	src := netip.AddrPortFrom(browser, 50000)
	intercept := func(dst string) net.Conn {
		a, b := net.Pipe()
		go w.eng.HandleIntercepted(context.Background(), b, src, netip.MustParseAddrPort(dst))
		return a
	}
	// Resolved by this workload from an allowed name: allowed.
	c := intercept("151.101.0.223:443")
	c.Write([]byte("x")) // not TLS; the name came from DNS
	expectGreeting(t, c, "pypi")
	c.Close()
	// Never resolved: refused.
	c = intercept("151.101.0.224:443")
	c.Write([]byte("x"))
	expectClosed(t, c)
	waitEvent(t, w.eng.Log(), func(e events.Event) bool {
		return e.Dest == "151.101.0.224:443" && e.Reason == policy.ReasonUnresolvedIP
	})
	// Opens like TLS, but no ClientHello can be read: refused.
	c = intercept("151.101.0.223:443")
	c.Write([]byte{0x16, 3, 1, 0, 8, 1, 0, 0, 4, 0xde, 0xad, 0xbe, 0xef})
	// The server speaks first, so its greeting may come back; the
	// connection must close without echoing anything of the client's.
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if back, err := io.ReadAll(c); err != nil || strings.Contains(string(back), "\xde\xad") || len(back) > len("pypi\n") {
		t.Errorf("unreadable: got %q back, %v", back, err)
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool {
		return e.Workload == "browser" && e.Entry == policy.EntryIntercept && e.Reason == policy.ReasonSNIUnreadable
	})
	// Tailnet: through the identity.
	c = intercept(netip.AddrPortFrom(w.net.Node("store").IP(), 8000).String())
	expectGreeting(t, c, "store")
	c.Close()
	// Explain agrees, without traffic.
	x, _ := w.eng.Explain(context.Background(), ExplainRequest{Workload: "browser", Dest: "151.101.0.223:443"})
	if x.Verdict != policy.Allow || x.ResolvedFrom[0] != "pypi.org" {
		t.Errorf("explain %+v", x)
	}
	x, _ = w.eng.Explain(context.Background(), ExplainRequest{Workload: "browser", Dest: "pypi.org:443", SNI: "evil.com"})
	if x.Reason != policy.ReasonSNINotAllowed {
		t.Errorf("explain sni %+v", x)
	}
}

func TestExitNodeFailsClosed(t *testing.T) {
	w := newWorld(t)
	exit := w.net.Node("exit-1")
	exit.OfferExitNode()
	w.start(t)
	vpn := w.net.Node("egress-vpn")
	waitFor(t, func() bool { return vpn.Status(context.Background()).ExitNode == "exit-1" })
	// The exit node is set on the egress's identity, not the workload's.
	if got := w.net.Node("work").Status(context.Background()).ExitNode; got != "" {
		t.Errorf("work's own identity has exit node %q", got)
	}
	src := netip.AddrPortFrom(netip.MustParseAddr("10.137.0.22"), 50000)
	intercept := func() net.Conn {
		a, b := net.Pipe()
		go w.eng.HandleIntercepted(context.Background(), b, src, netip.MustParseAddrPort("93.184.216.34:443"))
		return a
	}
	c := intercept()
	expectGreeting(t, c, "via-exit")
	c.Close()

	exit.SetOffline(true)
	c = intercept()
	expectClosed(t, c)
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Error != "" })
	if ev.Path != policy.PathExit {
		t.Errorf("%+v", ev)
	}
	if n := w.internet.dials("93.184.216.34:443"); n != 0 {
		t.Errorf("fell back to the local network %d times", n)
	}
}

func TestNoHealthyExitIsRefusedWithoutDialling(t *testing.T) {
	w := newWorld(t) // neither exit node offers to be one
	w.start(t)
	a, b := net.Pipe()
	go w.eng.HandleIntercepted(context.Background(), b, netip.MustParseAddrPort("10.137.0.22:1"), netip.MustParseAddrPort("93.184.216.34:443"))
	expectClosed(t, a)
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "work" })
	if ev.Verdict != policy.Refuse || ev.Reason != policy.ReasonNoHealthyExit || ev.Identity != "egress-vpn" {
		t.Errorf("%+v", ev)
	}
	if n := w.internet.dials("93.184.216.34:443"); n != 0 {
		t.Errorf("fell back to the local network")
	}
	st := w.eng.Status(context.Background()).Egresses["vpn"]
	if st.Usable || st.Active != "" || len(st.Exits) != 2 || st.Identity == nil {
		t.Errorf("status %+v", st)
	}
	// DNS through the egress: SERVFAIL, not NXDOMAIN, and nothing asked locally.
	q := dnsQuery(t, "example.com", dnsmessage.TypeA)
	if rc, _ := dnsAnswer(t, w.eng.AnswerDNS(context.Background(), netip.MustParseAddr("10.137.0.22"), q)); rc != dnsmessage.RCodeServerFailure {
		t.Errorf("rcode %v", rc)
	}
}

func TestUnlistedSourcesUseTheirEgress(t *testing.T) {
	w := newWorld(t)
	w.net.Node("exit-1").OfferExitNode()
	w.cfg.Unlisted = &config.Unlisted{Internet: config.InternetFull, Egress: "vpn"}
	w.start(t)
	waitFor(t, func() bool { return w.eng.Status(context.Background()).Egresses["vpn"].Usable })
	a, b := net.Pipe()
	go w.eng.HandleIntercepted(context.Background(), b, netip.MustParseAddrPort("10.137.0.99:1"), netip.MustParseAddrPort("93.184.216.34:443"))
	expectGreeting(t, a, "via-exit")
	a.Close()
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "unlisted:10.137.0.99" })
	if ev.Verdict != policy.Allow || ev.Path != policy.PathExit || ev.Identity != "egress-vpn" {
		t.Errorf("%+v", ev)
	}
	// A tailnet address is never reached through the egress.
	a, b = net.Pipe()
	go w.eng.HandleIntercepted(context.Background(), b, netip.MustParseAddrPort("10.137.0.99:2"),
		netip.AddrPortFrom(w.net.Node("airgap").IP(), 443))
	expectClosed(t, a)
}

func TestSignInLinkInStatus(t *testing.T) {
	w := newWorld(t)
	airgap := w.net.Node("airgap")
	airgap.NeedsLogin()
	w.start(t)
	st := w.eng.Status(context.Background())
	id := st.Workloads["airgap"].Identity
	if id.State != "NeedsLogin" || id.AuthURL == "" {
		t.Fatalf("%+v", id)
	}
	airgap.Approve()
	if id := w.eng.Status(context.Background()).Workloads["airgap"].Identity; id.State != "Running" || id.AuthURL != "" {
		t.Errorf("%+v", id)
	}
	if st.Version != "t1" {
		t.Errorf("version %q", st.Version)
	}
}

func TestUpstreamUnreachable(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223") // nothing listens
	if _, err := w.socksDial("job", "pypi.org:443"); err == nil {
		t.Fatal("connected")
	}
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Error != "" })
	if ev.Verdict != policy.Allow {
		t.Errorf("an allowed connection that failed keeps its verdict: %+v", ev)
	}
}

// ---- fakes ----

type dialFunc func(ctx context.Context, n, a string) (net.Conn, error)

func (f dialFunc) Dial(n, a string) (net.Conn, error) { return f(context.Background(), n, a) }
func (f dialFunc) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return f(ctx, n, a)
}

// fakeInternet maps addresses to loopback servers and names to addresses.
type fakeInternet struct {
	mu      sync.Mutex
	servers map[string]string
	names   map[string][]netip.Addr
	count   map[string]int
}

func newFakeInternet(t *testing.T) *fakeInternet {
	return &fakeInternet{servers: map[string]string{}, names: map[string][]netip.Addr{}, count: map[string]int{}}
}

func (f *fakeInternet) serve(t *testing.T, addr, greeting string) {
	f.serveRaw(t, addr, func(c net.Conn) { fmt.Fprintf(c, "%s\n", greeting); io.Copy(c, c) })
}

func (f *fakeInternet) serveRaw(t *testing.T, addr string, h func(net.Conn)) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go acceptLoop(ln, h)
	f.mu.Lock()
	f.servers[addr] = ln.Addr().String()
	f.mu.Unlock()
}

func (f *fakeInternet) name(n string, addrs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range addrs {
		f.names[n] = append(f.names[n], netip.MustParseAddr(a))
	}
}

func (f *fakeInternet) dials(addr string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.count[addr] }

func (f *fakeInternet) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	f.mu.Lock()
	f.count[addr]++
	real, ok := f.servers[addr]
	f.mu.Unlock()
	if !ok {
		return nil, errors.New("fake internet: connection refused")
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", real)
}

func (f *fakeInternet) dial(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	return f.DialContext(ctx, "tcp", dst.String())
}

func (f *fakeInternet) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.names[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var out []netip.Addr
	for _, x := range a {
		if network == "ip" || (network == "ip4") == x.Is4() {
			out = append(out, x)
		}
	}
	return out, nil
}

type fakeQubes struct {
	t       *testing.T
	mu      sync.Mutex
	servers map[string]string
	calls   atomic.Int32
}

func (q *fakeQubes) serve(t *testing.T, qube string, port uint16, greeting string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go acceptLoop(ln, func(c net.Conn) { fmt.Fprintf(c, "%s\n", greeting); io.Copy(c, c) })
	q.mu.Lock()
	q.servers[fmt.Sprintf("%s:%d", qube, port)] = ln.Addr().String()
	q.mu.Unlock()
}

func (q *fakeQubes) DialQube(ctx context.Context, qube string, port uint16) (net.Conn, error) {
	q.calls.Add(1)
	q.mu.Lock()
	a, ok := q.servers[fmt.Sprintf("%s:%d", qube, port)]
	q.mu.Unlock()
	if !ok {
		return nil, errors.New("qrexec: refused")
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", a)
}

func serveFake(t *testing.T, id *tailnet.Fake, port uint16, greeting string) {
	ln, err := id.Listen(port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go acceptLoop(ln, func(c net.Conn) { fmt.Fprintf(c, "%s\n", greeting); io.Copy(c, c) })
}

func acceptLoop(ln net.Listener, h func(net.Conn)) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() { defer c.Close(); h(c) }()
	}
}

func expectGreeting(t *testing.T, c net.Conn, want string) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != want {
		t.Fatalf("greeting %q %v, want %q", line, err, want)
	}
	c.SetReadDeadline(time.Time{})
}

func echo(t *testing.T, c net.Conn) {
	t.Helper()
	go c.Write([]byte("ping\n"))
	expectGreeting(t, c, "ping")
}

func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b := make([]byte, 64)
	n, err := c.Read(b)
	if err == nil {
		t.Fatalf("read %q, want closed", b[:n])
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("still open")
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

// A torii.Connect call is decided against the qube dom0 named; one from a
// routed qube, or from a qube the file does not list, is refused.
func TestQrexecCallerMustBeAQrexecWorkload(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_7C3Y, "routed-caller-refused")
	w := newWorld(t)
	w.start(t)
	for caller, reason := range map[string]policy.Reason{"browser": policy.ReasonWrongEntry, "nobody": policy.ReasonUnknownWorkload} {
		a, b := net.Pipe()
		go w.eng.HandleQrexec(context.Background(), caller, "socks", b)
		expectClosed(t, a)
		ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == caller })
		if ev.Verdict != policy.Refuse || ev.Reason != reason {
			t.Errorf("%s: %+v, want refused with %s", caller, ev, reason)
		}
	}
}

// A refusal is logged with the verdict, the reason, and what was asked.
func TestARefusalIsLoggedWithItsReason(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_R4CD, "refusal-has-reason")
	w := newWorld(t)
	w.start(t)
	if c, err := w.socksDial("job", "evil.com:443"); err == nil {
		c.Close()
		t.Fatal("evil.com allowed")
	}
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "job" && e.Dest == "evil.com:443" })
	if ev.Verdict != policy.Refuse || ev.Reason != policy.ReasonNotAllowlisted || ev.Entry != policy.EntrySOCKS ||
		ev.Direction != events.Out || ev.Time.IsZero() {
		t.Errorf("event %+v", ev)
	}
}

// explain gives a real connection's verdict and reason, and dials nothing.
func TestExplainAgreesAndDialsNothing(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_Z5ST, "explain-agrees", "explain-dials-nothing")
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.serve(t, "151.101.0.223:443", "pypi")
	dests := []string{"pypi.org:443", "evil.com:443"}
	xs := map[string]Explanation{}
	for _, dest := range dests {
		x, err := w.eng.Explain(context.Background(), ExplainRequest{Workload: "job", Dest: dest})
		if err != nil {
			t.Fatal(err)
		}
		xs[dest] = x
	}
	if n := w.internet.dials("151.101.0.223:443"); n != 0 {
		t.Fatalf("explain dialled %d times", n)
	}
	for _, dest := range dests {
		if c, err := w.socksDial("job", dest); err == nil {
			c.Write([]byte("hi\n")) // not TLS: decided on the name
			expectGreeting(t, c, "pypi")
			c.Close()
		}
		ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "job" && e.Dest == dest })
		if x := xs[dest]; x.Verdict != ev.Verdict || x.Reason != ev.Reason {
			t.Errorf("%s: explain %s %s, the connection %s %s", dest, x.Verdict, x.Reason, ev.Verdict, ev.Reason)
		}
	}
	if xs["pypi.org:443"].Verdict != policy.Allow || xs["evil.com:443"].Verdict != policy.Refuse {
		t.Errorf("explanations %+v", xs)
	}
}
