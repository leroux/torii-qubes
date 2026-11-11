package engine

// The simulated machine: every cell of a machine file, run through the real
// engine. A cell is one source workload, one way out of it (intercept for a
// routed qube; SOCKS, the HTTP proxy or a forward for one with no network
// card; inbound from a tailnet peer), and one destination.
//
// The machine file is turned into the proxy's config by the dom0 tool's own
// derive (Python), so this is the config Qubes would get. Around the engine
// everything is fake: the tailnet (FakeNet, its grants from a policy file),
// the internet, the exit node's internet, the LAN and the qubes. Each
// destination answers with a marker only it serves; a cell is reached when
// its marker comes back through the engine.
//
// The expected verdict of a cell is the engine's own decision (Explain),
// and for tailnet destinations also the grants. What this does not test is
// everything outside the engine: Qubes (qrexec policy, firewall, QubesDB),
// the kernel's rules, real Tailscale, the dom0 tool against real qubes, and
// the packages.

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"

	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/config"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/events"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/policy"
	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/tailnet"
)

const (
	simPeer    = "lintel-peer"
	simProxyIP = "10.137.0.30"
	simSuffix  = "fake.ts.net"
)

var simPeerTags = []string{"tag:lintel-peer", "tag:lintel-exit"}

type simPolicy struct {
	Grants []struct {
		Src []string `json:"src"`
		Dst []string `json:"dst"`
		IP  []string `json:"ip"`
	} `json:"grants"`
}

func (p simPolicy) granted(src, dst []string, port uint16) bool {
	has := func(a, b []string) bool {
		for _, x := range a {
			for _, y := range b {
				if x == y {
					return true
				}
			}
		}
		return false
	}
	for _, g := range p.Grants {
		if !has(g.Src, src) || !has(g.Dst, dst) {
			continue
		}
		for _, ip := range g.IP {
			if ip == "*" || ip == fmt.Sprintf("tcp:%d", port) {
				return true
			}
		}
	}
	return false
}

// simConfig derives the proxy's config from the machine file with the dom0
// tool's own code.
func simConfig(t *testing.T) *config.Config {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 for the dom0 tool")
	}
	script := `import json, sys
sys.path.insert(0, "../../../dom0")
from lintel import machine, derive
m = machine.load("testdata/sim/machine.yaml")
addrs, n = {}, 20
for name, q in sorted(m.qubes.items()):
    if q.network_card:
        addrs[name] = "10.137.0.%d" % n
        n += 1
print(json.dumps(derive.proxy_config(m, m.default_proxy, addrs, "sim")))`
	out, err := exec.Command(py, "-c", script).Output()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	cfg, err := config.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

type simDest struct {
	kind   string // internet, lan, qube, proxy, tailnet
	host   string // a name, an address, or a tailnet device's short name
	port   uint16
	marker string // what the destination answers
	tls    bool
	tags   []string // tailnet destinations: the device's tags
}

type simCell struct {
	src, channel string // channel: intercept, socks, http, f.<name>, in
	dest         simDest
	want, got    string // reached | refused
	why          string
}

func (c simCell) id() string {
	return fmt.Sprintf("%s/%s/%s/%s:%d", c.src, c.channel, c.dest.kind, c.dest.host, c.dest.port)
}

type sim struct {
	t        *testing.T
	cfg      *config.Config
	eng      *Engine
	net      *tailnet.FakeNet
	inet     *fakeInternet
	exitNet  *fakeInternet
	qubes    *fakeQubes
	pol      simPolicy
	tags     map[string][]string // tailnet hostname -> tags
	hostname map[string]string   // workload -> its tailnet hostname
	addr     map[string]netip.Addr
	dests    []simDest
}

func newSim(t *testing.T) *sim {
	cfg := simConfig(t)
	b, err := os.ReadFile("testdata/sim/tailnet-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	s := &sim{t: t, cfg: cfg, inet: newFakeInternet(t), exitNet: newFakeInternet(t),
		qubes: &fakeQubes{t: t, servers: map[string]string{}},
		tags:  map[string][]string{simPeer: simPeerTags}, hostname: map[string]string{}, addr: map[string]netip.Addr{}}
	if err := json.Unmarshal(b, &s.pol); err != nil {
		t.Fatal(err)
	}
	s.net = &tailnet.FakeNet{Suffix: simSuffix, Deny: func(from, to string, port uint16) bool {
		return !s.pol.granted(s.tags[from], s.tags[to], port)
	}}
	s.net.Internet = s.exitNet.dial
	ids := map[string]tailnet.Identity{}
	for _, name := range cfg.WorkloadNames() {
		w := cfg.Workloads[name]
		if w.Address != "" {
			s.addr[name] = netip.MustParseAddr(w.Address)
		}
		if w.Identity == nil {
			continue
		}
		ids[name] = s.net.Node(w.Identity.Hostname)
		s.hostname[name] = w.Identity.Hostname
		s.tags[w.Identity.Hostname] = w.Identity.Tags
	}
	for _, name := range cfg.EgressNames() {
		g := cfg.Egresses[name]
		ids[config.EgressIdentityKey(name)] = s.net.Node(g.Hostname)
		s.tags[g.Hostname] = g.Tags
	}
	peer := s.net.Node(simPeer)
	peer.OfferExitNode()
	serveFake(t, peer, 7000, "peer 7000")
	serveFake(t, peer, 22, "peer 22")

	// The internet: every name the file lists, one it does not, a raw
	// address; the same names behind the exit node answer "exit".
	names := map[string]bool{"wikipedia.org": true}
	lans := map[string]bool{"192.168.1.1:80": true}
	for _, w := range cfg.Workloads {
		for _, e := range w.InternetAllow {
			if a, err := config.ParseAllow(e); err == nil && a.Host != "" && !strings.HasPrefix(a.Host, "*.") {
				names[a.Host] = true
			}
		}
		for _, e := range w.LANAllow {
			if a, err := config.ParseAllow(e); err == nil && a.Prefix.IsValid() && a.Prefix.IsSingleIP() && a.Port != 0 {
				lans[fmt.Sprintf("%s:%d", a.Prefix.Addr(), a.Port)] = true
			}
		}
	}
	i := 10
	for _, n := range sortedKeys(names) {
		ip := fmt.Sprintf("93.184.216.%d", i)
		i++
		s.inet.name(n, ip)
		s.exitNet.name(n, ip)
		s.inet.serve(t, ip+":443", "net "+n)
		s.exitNet.serve(t, ip+":443", "exit "+n)
		s.dests = append(s.dests, simDest{kind: "internet", host: n, port: 443, marker: n})
	}
	s.inet.serve(t, "1.1.1.1:443", "net 1.1.1.1")
	s.exitNet.serve(t, "1.1.1.1:443", "exit 1.1.1.1")
	s.dests = append(s.dests, simDest{kind: "internet", host: "1.1.1.1", port: 443, marker: "1.1.1.1"})
	for _, hp := range sortedKeys(lans) {
		ap := netip.MustParseAddrPort(hp)
		s.inet.serve(t, hp, "lan "+hp)
		s.dests = append(s.dests, simDest{kind: "lan", host: ap.Addr().String(), port: ap.Port(), marker: "lan " + hp})
	}
	s.inet.serve(t, "10.137.0.28:7000", "qube")
	s.inet.serve(t, simProxyIP+":15001", "self")
	s.dests = append(s.dests,
		simDest{kind: "qube", host: "10.137.0.28", port: 7000, marker: "qube"},
		simDest{kind: "proxy", host: simProxyIP, port: 15001, marker: "self"},
		simDest{kind: "tailnet", host: simPeer, port: 7000, marker: "peer 7000", tags: simPeerTags},
		simDest{kind: "tailnet", host: simPeer, port: 22, marker: "peer 22", tags: simPeerTags})

	// Every identity's inbound ports: a marker in the qube behind each, and a
	// tailnet destination for the other workloads.
	for _, name := range cfg.WorkloadNames() {
		w := cfg.Workloads[name]
		if w.Identity == nil {
			continue
		}
		qube := name
		if w.Qube != "" {
			qube = w.Qube
		}
		for _, in := range w.Inbound {
			marker := fmt.Sprintf("in %s:%d", qube, in.TargetPort())
			if _, ok := s.qubes.servers[fmt.Sprintf("%s:%d", qube, in.TargetPort())]; !ok {
				s.qubes.serve(t, qube, in.TargetPort(), marker)
			}
			s.dests = append(s.dests, simDest{kind: "tailnet", host: w.Identity.Hostname, port: in.Port,
				marker: marker, tls: in.TLS, tags: w.Identity.Tags})
		}
	}

	s.eng = New(Deps{
		Config: cfg, Identities: ids, Internet: s.inet, Resolver: s.inet, Qubes: s.qubes, Local: s.inet,
		ExitResolver: func(tailnet.Identity) Resolver { return s.exitNet },
		SelfAddrs:    func() []netip.Addr { return []netip.Addr{netip.MustParseAddr(simProxyIP)} },
		Log:          &events.Log{}, SNIWait: 150 * time.Millisecond, DialTimeout: time.Second,
	})
	t.Cleanup(func() { s.eng.Close() })
	if err := s.eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Exit-path connections are refused until each egress has checked its
	// exit nodes once.
	deadline := time.Now().Add(5 * time.Second)
	for _, g := range s.eng.Status(context.Background()).Egresses {
		for !s.eng.Status(context.Background()).Egresses[g.Name].Usable {
			if time.Now().After(deadline) {
				t.Fatalf("egress %s never usable: %+v", g.Name, s.eng.Status(context.Background()).Egresses[g.Name])
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cells: every source x way out x destination, and every inbound port.
func (s *sim) cells() []simCell {
	var out []simCell
	for _, name := range s.cfg.WorkloadNames() {
		w := s.cfg.Workloads[name]
		if w.Qube != "" {
			continue // a service: inbound only
		}
		var channels []string
		switch w.Link {
		case config.LinkRouted:
			channels = []string{"intercept"}
		case config.LinkQrexec:
			if w.SOCKS {
				channels = append(channels, "socks")
			}
			if w.HTTP {
				channels = append(channels, "http")
			}
		}
		for _, ch := range channels {
			for _, d := range s.dests {
				if d.kind == "tailnet" && d.host == s.hostname[name] {
					continue
				}
				out = append(out, simCell{src: name, channel: ch, dest: d})
			}
		}
		for _, f := range w.Forwards {
			host, port, _ := config.SplitHostPort(f.To)
			d := simDest{kind: "internet", host: host, port: port}
			for _, x := range s.dests {
				if x.host == host && x.port == port {
					d = x
				}
			}
			out = append(out, simCell{src: name, channel: "f." + f.Name, dest: d})
		}
	}
	// Inbound from the peer: every declared port, and one that is not.
	for _, name := range s.cfg.WorkloadNames() {
		w := s.cfg.Workloads[name]
		if w.Identity == nil || len(w.Inbound) == 0 {
			continue
		}
		for _, d := range s.dests {
			if d.kind == "tailnet" && d.host == w.Identity.Hostname {
				out = append(out, simCell{src: simPeer, channel: "in", dest: d})
			}
		}
		out = append(out, simCell{src: simPeer, channel: "in",
			dest: simDest{kind: "tailnet", host: w.Identity.Hostname, port: 22, marker: "none", tags: w.Identity.Tags}})
	}
	return out
}

// expect: the engine's own decision, and the grants for tailnet destinations.
func (s *sim) expect(c *simCell) {
	ctx := context.Background()
	if c.channel == "in" {
		d, _ := policy.DecideInbound(s.cfg, s.workloadOf(c.dest.host), c.dest.port)
		ok := d.Verdict == policy.Allow && s.pol.granted(simPeerTags, c.dest.tags, c.dest.port)
		c.want, c.why = verdict(ok), fmt.Sprintf("inbound %s; grant %v", d.Reason, s.pol.granted(simPeerTags, c.dest.tags, c.dest.port))
		return
	}
	host := c.dest.host
	if c.dest.kind == "tailnet" && s.cfg.Workloads[c.src].Link == config.LinkRouted {
		host += "." + simSuffix
	}
	x, err := s.eng.Explain(ctx, ExplainRequest{Workload: c.src, Dest: fmt.Sprintf("%s:%d", host, c.dest.port)})
	if err != nil {
		c.want, c.why = "refused", "explain: "+err.Error()
		return
	}
	ok := x.Verdict == policy.Allow
	c.why = fmt.Sprintf("engine %s %s", x.Verdict, x.Reason)
	if c.dest.kind == "tailnet" {
		g := s.pol.granted(s.tags[s.hostname[c.src]], c.dest.tags, c.dest.port)
		ok = ok && g
		c.why += fmt.Sprintf("; grant %v", g)
	}
	c.want = verdict(ok)
}

func (s *sim) workloadOf(hostname string) string {
	for n, h := range s.hostname {
		if h == hostname {
			return n
		}
	}
	return ""
}

func verdict(ok bool) string {
	if ok {
		return "reached"
	}
	return "refused"
}

// measure sends the cell's traffic through the engine and reads its marker.
func (s *sim) measure(c *simCell) {
	ctx := context.Background()
	conn, err := s.connect(ctx, c)
	if err != nil || conn == nil {
		c.got = "refused"
		return
	}
	defer conn.Close()
	if c.dest.tls {
		tc := tls.Client(conn, &tls.Config{ServerName: c.dest.host + "." + simSuffix, InsecureSkipVerify: true})
		tc.SetDeadline(time.Now().Add(3 * time.Second))
		if tc.Handshake() != nil {
			c.got = "refused"
			return
		}
		conn = tc
	} else if c.channel == "intercept" {
		conn.Write([]byte("x")) // not TLS: no server name to wait for
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	line = strings.TrimSpace(line)
	c.got = verdict(line != "" && strings.Contains(line, c.dest.marker))
	// Through an exit node, the internet answers from the exit node's side.
	if c.got == "reached" && c.dest.kind == "internet" && c.channel != "in" {
		if w := s.cfg.Workloads[c.src]; w.Egress != "" && !strings.HasPrefix(line, "exit ") {
			c.got, c.why = "refused", c.why+"; went direct, not through the exit node"
		} else if w.Egress == "" && strings.HasPrefix(line, "exit ") {
			c.got, c.why = "refused", c.why+"; went through an exit node it does not have"
		}
	}
}

func (s *sim) connect(ctx context.Context, c *simCell) (net.Conn, error) {
	target := fmt.Sprintf("%s:%d", c.dest.host, c.dest.port)
	switch {
	case c.channel == "in":
		peer := s.net.Node(simPeer)
		addrs, ok := peer.LookupTailnet(ctx, c.dest.host)
		if !ok || len(addrs) == 0 {
			return nil, fmt.Errorf("no such device")
		}
		return peer.Dial(ctx, netip.AddrPortFrom(addrs[0], c.dest.port))
	case c.channel == "socks":
		d, _ := proxy.SOCKS5("tcp", "qrexec", nil, dialFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
			a, b := net.Pipe()
			go s.eng.HandleQrexec(context.Background(), c.src, "socks", b)
			return a, nil
		}))
		return d.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
	case c.channel == "http":
		a, b := net.Pipe()
		go s.eng.HandleQrexec(context.Background(), c.src, "http", b)
		a.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprintf(a, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		br := bufio.NewReader(a)
		resp, err := http.ReadResponse(br, nil)
		if err != nil || resp.StatusCode != 200 {
			a.Close()
			return nil, fmt.Errorf("CONNECT: %v", err)
		}
		a.SetDeadline(time.Time{})
		return &readerConn{Conn: a, r: br}, nil
	case strings.HasPrefix(c.channel, "f."):
		a, b := net.Pipe()
		go s.eng.HandleQrexec(context.Background(), c.src, c.channel, b)
		return a, nil
	case c.channel == "intercept":
		src := netip.AddrPortFrom(s.addr[c.src], 50000)
		dst, err := netip.ParseAddr(c.dest.host)
		if err != nil {
			// A name: ask the program's DNS first, as the workload would.
			name := c.dest.host
			if c.dest.kind == "tailnet" {
				name += "." + simSuffix
			}
			q := dnsQuery(s.t, name, dnsmessage.TypeA)
			rc, addrs := dnsAnswer(s.t, s.eng.AnswerDNS(ctx, s.addr[c.src], q))
			if rc != dnsmessage.RCodeSuccess || len(addrs) == 0 {
				return nil, fmt.Errorf("dns: %v", rc)
			}
			dst = addrs[0]
		}
		a, b := net.Pipe()
		go s.eng.HandleIntercepted(context.Background(), b, src, netip.AddrPortFrom(dst, c.dest.port))
		return a, nil
	}
	return nil, fmt.Errorf("channel %s", c.channel)
}

// TestSimulatedMachine runs every cell of testdata/sim/machine.yaml.
func TestSimulatedMachine(t *testing.T) {
	s := newSim(t)
	cells := s.cells()
	byKind := map[string]int{}
	var wrong []simCell
	reached := 0
	for i := range cells {
		c := &cells[i]
		s.expect(c)
		s.measure(c)
		byKind[c.src+"/"+c.channel]++
		if c.got == "reached" {
			reached++
		}
		if c.got != c.want {
			wrong = append(wrong, *c)
		}
	}
	for _, c := range wrong {
		t.Errorf("%s: want %s, got %s (%s)", c.id(), c.want, c.got, c.why)
	}
	// The run must not be degenerate: some cells reached, some refused, and
	// every source exercised on each of its ways out.
	if reached < 10 || reached > len(cells)-10 {
		t.Errorf("%d of %d cells reached: the simulation is not exercising both sides", reached, len(cells))
	}
	for _, name := range s.cfg.WorkloadNames() {
		w := s.cfg.Workloads[name]
		if w.Qube != "" {
			continue
		}
		for _, ch := range map[config.Link][]string{config.LinkRouted: {"intercept"}, config.LinkQrexec: {"socks", "http"}}[w.Link] {
			if (ch == "socks" && !w.SOCKS) || (ch == "http" && !w.HTTP) {
				continue
			}
			if byKind[name+"/"+ch] == 0 {
				t.Errorf("no cells for %s over %s", name, ch)
			}
		}
	}
	t.Logf("%d cells, %d reached, %d not as expected", len(cells), reached, len(wrong))
	keys := make([]string, 0, len(byKind))
	for k := range byKind {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %-32s %d cells", k, byKind[k])
	}
}
