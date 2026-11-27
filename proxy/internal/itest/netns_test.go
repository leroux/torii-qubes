package itest

// Network namespaces as fake qubes. Routed workloads sit behind "vif"
// veth interfaces of a proxy namespace, whose own netvm is a namespace
// standing in for the internet. The proxy runs the real daemon with
// nftables interception and forwarding off; the tailnet is FakeNet,
// inside the proxy process (WireGuard is covered by the other tests).
//
// Needs root, ip and nft. The test binary re-executes itself in each
// namespace in a helper role chosen by TORII_HELPER.

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/leroux/torii-qubes/claims"

	"github.com/leroux/torii-qubes/proxy/internal/admin"
	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/daemon"
	"github.com/leroux/torii-qubes/proxy/internal/engine"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

func TestMain(m *testing.M) {
	switch os.Getenv("TORII_HELPER") {
	case "":
		os.Exit(m.Run())
	case "server":
		helperServer()
	case "udpecho":
		helperUDPEcho()
	case "dns":
		helperDNS()
	case "proxy":
		helperProxy()
	case "client":
		helperClient()
	}
	os.Exit(0)
}

// ---- helpers, run inside namespaces ----

func helperServer() {
	for _, a := range strings.Split(os.Getenv("ADDRS"), ",") {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			fmt.Println("listen:", err)
			os.Exit(1)
		}
		go func(ln net.Listener, a string) {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer c.Close()
					fmt.Fprintf(c, "server %s from %s\n", a, c.RemoteAddr())
					io.Copy(io.Discard, c)
				}()
			}
		}(ln, a)
	}
	fmt.Println("ready")
	select {}
}

func helperUDPEcho() {
	pc, err := net.ListenPacket("udp", os.Getenv("ADDR"))
	if err != nil {
		os.Exit(1)
	}
	fmt.Println("ready")
	b := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(b)
		if err != nil {
			return
		}
		pc.WriteTo([]byte(fmt.Sprintf("%s from %s", b[:n], from)), from)
	}
}

// helperDNS answers A queries from NAMES ("name=ip,...").
func helperDNS() {
	names := map[string]netip.Addr{}
	for _, kv := range strings.Split(os.Getenv("NAMES"), ",") {
		k, v, _ := strings.Cut(kv, "=")
		names[k+"."] = netip.MustParseAddr(v)
	}
	pc, err := net.ListenPacket("udp", os.Getenv("ADDR"))
	if err != nil {
		os.Exit(1)
	}
	fmt.Println("ready")
	b := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(b)
		if err != nil {
			return
		}
		var m dnsmessage.Message
		if m.Unpack(b[:n]) != nil || len(m.Questions) != 1 {
			continue
		}
		q := m.Questions[0]
		m.Response, m.RecursionAvailable = true, true
		if a, ok := names[strings.ToLower(q.Name.String())]; ok && q.Type == dnsmessage.TypeA {
			m.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: q.Class, TTL: 60},
				Body: &dnsmessage.AResource{A: a.As4()}}}
		} else if !ok {
			m.RCode = dnsmessage.RCodeNameError
		}
		out, _ := m.Pack()
		pc.WriteTo(out, from)
	}
}

// helperProxy runs the daemon with a fake tailnet holding store and a
// laptop; a test socket lets the harness dial from the laptop.
func helperProxy() {
	cfg, err := config.Load(os.Getenv("CONFIG"))
	if err != nil {
		fmt.Println("config:", err)
		os.Exit(1)
	}
	fn := &tailnet.FakeNet{Suffix: "tail.test"}
	ids := map[string]tailnet.Identity{}
	for _, n := range cfg.WorkloadNames() {
		if w := cfg.Workloads[n]; w.Identity != nil {
			ids[n] = fn.Node(w.Identity.Hostname)
		}
	}
	store := fn.Node("store")
	ln, _ := store.Listen(8000)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			fmt.Fprintf(c, "store\n")
			c.Close()
		}
	}()
	upc, _ := store.ListenUDP(5353)
	go func() {
		b := make([]byte, 1500)
		for {
			n, from, err := upc.ReadFrom(b)
			if err != nil {
				return
			}
			upc.WriteTo([]byte("store-udp:"+string(b[:n])), from)
		}
	}()
	laptop := fn.Node("laptop")
	d, err := daemon.Start(context.Background(), cfg, daemon.Options{
		Identities: ids, InstallRules: true, Downstream: "vif*", Logf: func(string, ...any) {},
	})
	if err != nil {
		fmt.Println("daemon:", err)
		os.Exit(1)
	}
	defer d.Close()
	// Test-only: GET /laptop?to=<hostname>:<port> dials from the laptop.
	tln, _ := net.Listen("unix", os.Getenv("TESTSOCK"))
	go http.Serve(tln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, _ := config.SplitHostPort(r.URL.Query().Get("to"))
		c, err := laptop.Dial(r.Context(), netip.AddrPortFrom(fn.Node(host).IP(), port))
		if err != nil {
			fmt.Fprintf(w, "error: %v", err)
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, _ := bufio.NewReader(c).ReadString('\n')
		fmt.Fprint(w, strings.TrimSpace(line))
	}))
	fmt.Printf("ready %s\n", store.IP())
	select {}
}

// helperClient: MODE=tcp|tls|udp|dns, TO=addr, SNI, NAME.
func helperClient() {
	to := os.Getenv("TO")
	switch os.Getenv("MODE") {
	case "tcp", "tls":
		c, err := net.DialTimeout("tcp", to, 5*time.Second)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		defer c.Close()
		if os.Getenv("MODE") == "tls" {
			tc := tls.Client(c, &tls.Config{ServerName: os.Getenv("SNI"), InsecureSkipVerify: true})
			tc.SetDeadline(time.Now().Add(3 * time.Second))
			go tc.Handshake() // the test servers are not TLS; only the ClientHello matters
		} else {
			c.Write([]byte("hi\n"))
		}
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(strings.TrimSpace(line))
	case "udp":
		c, _ := net.Dial("udp", to)
		c.Write([]byte("ping"))
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, 64)
		n, err := c.Read(b)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(string(b[:n]))
	case "dns":
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, n, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, n, to)
		}}
		a, err := r.LookupNetIP(context.Background(), "ip4", os.Getenv("NAME"))
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(a[0])
	}
}

// ---- the harness ----

type nsEnv struct {
	t      *testing.T
	prefix string
	self   string
	dir    string
	procs  []*exec.Cmd
}

func sh(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func (e *nsEnv) ns(n string) string { return e.prefix + n }

func (e *nsEnv) in(ns string, args ...string) {
	e.t.Helper()
	sh(e.t, append([]string{"ip", "netns", "exec", e.ns(ns)}, args...)...)
}

// start runs a helper in ns and waits for its "ready" line.
func (e *nsEnv) start(ns, role string, env ...string) string {
	e.t.Helper()
	cmd := exec.Command("ip", "netns", "exec", e.ns(ns), e.self)
	cmd.Env = append(os.Environ(), append([]string{"TORII_HELPER=" + role}, env...)...)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.procs = append(e.procs, cmd)
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "ready") {
		e.t.Fatalf("%s in %s: %q %v", role, ns, line, err)
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "ready"))
}

// run runs a one-shot client helper in ns and returns its output.
func (e *nsEnv) client(ns string, env ...string) string {
	e.t.Helper()
	cmd := exec.Command("ip", "netns", "exec", e.ns(ns), e.self)
	cmd.Env = append(os.Environ(), append([]string{"TORII_HELPER=client"}, env...)...)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out))
}

// realSetup reports whether checks that need a real Qubes machine, or a
// kernel whose veths and TPROXY behave as a Qubes netvm's do, should run:
// TORII_NETNS_REAL=1. In containers they fail for reasons of the
// container, so elsewhere they are skipped and say so.
func realSetup(t *testing.T, what string) bool {
	t.Helper()
	if os.Getenv("TORII_NETNS_REAL") == "1" {
		return true
	}
	t.Logf("skipped %s: needs a real setup (TORII_NETNS_REAL=1)", what)
	return false
}

func TestNetnsFakeQubes(t *testing.T) {
	if testing.Short() {
		t.Skip("creates network namespaces")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	for _, bin := range []string{"ip", "nft"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("needs " + bin)
		}
	}
	self, _ := os.Executable()
	e := &nsEnv{t: t, prefix: fmt.Sprintf("qm%d-", os.Getpid()%10000), self: self, dir: t.TempDir()}
	t.Cleanup(func() {
		for _, p := range e.procs {
			p.Process.Kill()
			p.Wait()
		}
		for _, n := range []string{"proxy", "inet", "browser", "web", "rogue"} {
			exec.Command("ip", "netns", "del", e.ns(n)).Run()
		}
		os.RemoveAll("/etc/netns/" + e.ns("proxy"))
	})
	for _, n := range []string{"proxy", "inet", "browser", "web", "rogue"} {
		sh(t, "ip", "netns", "add", e.ns(n))
		e.in(n, "ip", "link", "set", "lo", "up")
	}
	// Workloads: vifN.0 in the proxy, eth0 in the workload, like Qubes.
	link := func(ws string, n int, wsIP string) {
		vif, peer := fmt.Sprintf("vif%d.0", n), fmt.Sprintf("qm%d-%d", os.Getpid()%10000, n)
		sh(t, "ip", "link", "add", peer, "type", "veth", "peer", "name", vif)
		sh(t, "ip", "link", "set", vif, "netns", e.ns("proxy"))
		sh(t, "ip", "link", "set", peer, "netns", e.ns(ws))
		e.in(ws, "ip", "link", "set", peer, "name", "eth0")
		e.in(ws, "ip", "addr", "add", wsIP+"/24", "dev", "eth0")
		e.in(ws, "ip", "link", "set", "eth0", "up")
		e.in(ws, "ip", "route", "add", "default", "via", fmt.Sprintf("10.137.%d.1", n))
		e.in("proxy", "ip", "addr", "add", fmt.Sprintf("10.137.%d.1/24", n), "dev", vif)
		e.in("proxy", "ip", "link", "set", vif, "up")
	}
	link("browser", 1, "10.137.1.20")
	link("web", 2, "10.137.2.21")
	link("rogue", 3, "10.137.3.66") // a qube the config does not know
	// The proxy's own netvm, and "the internet" behind it.
	up := fmt.Sprintf("qm%d-up", os.Getpid()%10000)
	sh(t, "ip", "link", "add", up, "type", "veth", "peer", "name", up+"x")
	sh(t, "ip", "link", "set", up, "netns", e.ns("proxy"))
	sh(t, "ip", "link", "set", up+"x", "netns", e.ns("inet"))
	e.in("proxy", "ip", "addr", "add", "192.168.100.2/24", "dev", up)
	e.in("proxy", "ip", "link", "set", up, "up")
	e.in("proxy", "ip", "route", "add", "default", "via", "192.168.100.1")
	e.in("inet", "ip", "addr", "add", "192.168.100.1/24", "dev", up+"x")
	e.in("inet", "ip", "link", "set", up+"x", "up")
	e.in("inet", "ip", "addr", "add", "203.0.113.10/32", "dev", "lo")
	e.in("inet", "ip", "addr", "add", "203.0.113.66/32", "dev", "lo")
	e.in("inet", "ip", "route", "add", "10.137.0.0/16", "via", "192.168.100.2")
	os.MkdirAll("/etc/netns/"+e.ns("proxy"), 0o755)
	os.WriteFile("/etc/netns/"+e.ns("proxy")+"/resolv.conf", []byte("nameserver 192.168.100.1\n"), 0o644)

	e.start("inet", "server", "ADDRS=203.0.113.10:443,203.0.113.10:80,203.0.113.66:443")
	e.start("inet", "udpecho", "ADDR=203.0.113.10:9999")
	e.start("inet", "udpecho", "ADDR=203.0.113.66:9999")
	e.start("inet", "dns", "ADDR=192.168.100.1:53", "NAMES=pypi.org=203.0.113.10,evil.com=203.0.113.66,www.example.com=203.0.113.10")
	e.start("web", "server", "ADDRS=0.0.0.0:80")

	cfgPath := filepath.Join(e.dir, "config.json")
	adminSock := filepath.Join(e.dir, "admin.sock")
	cfg := map[string]any{
		"version": "netns", "proxy_qube": "sys-torii", "state_dir": e.dir,
		"listen": map[string]string{"admin": adminSock, "connect": filepath.Join(e.dir, "connect.sock")},
		"workloads": map[string]any{
			"browser": map[string]any{"link": "routed", "address": "10.137.1.20", "identity": map[string]any{"tags": []string{"tag:browser"}},
				"tailnet_outbound": true, "internet": "allowlist", "internet_allow": []string{"pypi.org"}},
			"web": map[string]any{"link": "routed", "address": "10.137.2.21", "identity": map[string]any{"tags": []string{"tag:web"}},
				"inbound": []map[string]int{{"port": 80}}, "internet": "full"},
		},
	}
	b, _ := json.Marshal(cfg)
	os.WriteFile(cfgPath, b, 0o644)
	testSock := filepath.Join(e.dir, "test.sock")
	storeIP := e.start("proxy", "proxy", "CONFIG="+cfgPath, "TESTSOCK="+testSock)
	proxyProc := e.procs[len(e.procs)-1]
	adm := admin.Client{Socket: adminSock}

	// check reports on the subtest it is given, so each subtest's outcome
	// is its own.
	check := func(t *testing.T, name, got, want string) {
		t.Helper()
		if !strings.HasPrefix(got, want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}

	t.Run("forwarding is off in the proxy", func(t *testing.T) {
		out, _ := exec.Command("ip", "netns", "exec", e.ns("proxy"), "cat", "/proc/sys/net/ipv4/ip_forward").Output()
		check(t, "ip_forward", strings.TrimSpace(string(out)), "0")
		// A routed qube outside the config gets no UDP through.
		check(t, "rogue udp", e.client("rogue", "MODE=udp", "TO=203.0.113.10:9999"), "error:")
	})

	t.Run("full internet is relayed, from the proxy's own address", func(t *testing.T) {
		got := e.client("web", "MODE=tcp", "TO=203.0.113.10:80")
		check(t, "web -> internet", got, "server 203.0.113.10:80 from 192.168.100.2:")
	})

	t.Run("dns through the program", func(t *testing.T) {
		check(t, "browser pypi.org", e.client("browser", "MODE=dns", "TO=10.139.1.1:53", "NAME=pypi.org"), "203.0.113.10")
		check(t, "browser evil.com", e.client("browser", "MODE=dns", "TO=10.139.1.1:53", "NAME=evil.com"), "error:")
		check(t, "browser tailnet", e.client("browser", "MODE=dns", "TO=10.139.1.1:53", "NAME=store.tail.test"), storeIP)
		check(t, "web www", e.client("web", "MODE=dns", "TO=10.139.1.1:53", "NAME=www.example.com"), "203.0.113.10")
		check(t, "rogue", e.client("rogue", "MODE=dns", "TO=10.139.1.1:53", "NAME=pypi.org"), "error:")
	})

	t.Run("allowlist by resolved name and server name", func(t *testing.T) {
		// browser resolved pypi.org above, so its address is allowed...
		check(t, "pypi", e.client("browser", "MODE=tls", "TO=203.0.113.10:443", "SNI=pypi.org"), "server 203.0.113.10:443")
		// ...but not for another server name on the same address. The test
		// server greets first, and the server's side flows before the
		// client's ClientHello is decided, so the greeting may come back:
		// the evidence is the refusal in the log.
		e.client("browser", "MODE=tls", "TO=203.0.113.10:443", "SNI=evil.com")
		var log []events.Event
		if err := adm.Get(context.Background(), "/log", nil, &log); err != nil {
			t.Fatal(err)
		}
		fronted := false
		for _, ev := range log {
			fronted = fronted || (ev.SNI == "evil.com" && ev.Verdict == policy.Refuse && ev.Reason == policy.ReasonSNINotAllowed)
		}
		if !fronted {
			t.Error("fronting: no refusal of server name evil.com in the log")
		}
		// and an address it never resolved is refused.
		check(t, "evil ip", e.client("browser", "MODE=tcp", "TO=203.0.113.66:443"), "error:")
	})

	t.Run("tailnet from a routed workload", func(t *testing.T) {
		check(t, "store", e.client("browser", "MODE=tcp", "TO="+storeIP+":8000"), "store")
		check(t, "web has no tailnet outbound", e.client("web", "MODE=tcp", "TO="+storeIP+":8000"), "error:")
	})

	t.Run("udp is relayed, decided and answered from the real peer", func(t *testing.T) {
		// The client's socket is connected, so it accepts the reply only
		// from 203.0.113.10:9999: the reply socket is bound there. The
		// server sees the proxy's address: relayed, not forwarded.
		check(t, "web udp", e.client("web", "MODE=udp", "TO=203.0.113.10:9999"), "ping from 192.168.100.2:")
		// browser resolved pypi.org (203.0.113.10) through the program...
		if realSetup(t, "the reply to a resolved name's UDP") {
			check(t, "browser udp resolved", e.client("browser", "MODE=udp", "TO=203.0.113.10:9999"), "ping from 192.168.100.2:")
		}
		// ...but never 203.0.113.66.
		check(t, "browser udp unresolved", e.client("browser", "MODE=udp", "TO=203.0.113.66:9999"), "error:")
		check(t, "browser udp tailnet", e.client("browser", "MODE=udp", "TO="+storeIP+":5353"), "store-udp:ping")
		check(t, "web udp tailnet", e.client("web", "MODE=udp", "TO="+storeIP+":5353"), "error:")
	})

	t.Run("an unknown source is refused", func(t *testing.T) {
		check(t, "rogue", e.client("rogue", "MODE=tcp", "TO=203.0.113.10:80"), "error:")
	})

	t.Run("inbound from the tailnet into a routed workload", func(t *testing.T) {
		if !realSetup(t, "inbound into a routed workload") {
			t.SkipNow()
		}
		c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", testSock)
		}}}
		resp, err := c.Get("http://x/laptop?to=web:80")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		check(t, "laptop -> web:80", string(body), "server 0.0.0.0:80 from 10.137.2.1:")
	})

	t.Run("the decision log and explain agree", func(t *testing.T) {
		var log []events.Event
		if err := adm.Get(context.Background(), "/log", nil, &log); err != nil {
			t.Fatal(err)
		}
		reasons := map[policy.Reason]bool{}
		for _, ev := range log {
			reasons[ev.Reason] = true
		}
		for _, r := range []policy.Reason{policy.ReasonSNINotAllowed, policy.ReasonUnresolvedIP, policy.ReasonNoTailnet,
			policy.ReasonUnknownWorkload, policy.ReasonInternetFull, policy.ReasonAllowlisted, policy.ReasonTailnetOutbound} {
			if !reasons[r] {
				t.Errorf("no %s event in the log", r)
			}
		}
		var x engine.Explanation
		q := map[string][]string{"workload": {"browser"}, "dest": {"203.0.113.10:443"}, "sni": {"evil.com"}}
		if err := adm.Get(context.Background(), "/explain", q, &x); err != nil {
			t.Fatal(err)
		}
		if x.Reason != policy.ReasonSNINotAllowed {
			t.Errorf("explain: %+v", x)
		}
	})

	// Last, as it stops the program: the guard alone keeps forwarding off.
	// The program's own rules are removed and the kernel's forwarding turned
	// back on, as a crashed program and Qubes' network setup would leave it.
	t.Run("the guard alone forwards nothing", func(t *testing.T) {
		claims.Verifies(t, claims.SW_261127_JWK2, "program-stopped")
		e.in("proxy", "nft", "-f", "../../../packaging/proxy/torii-guard.nft")
		proxyProc.Process.Kill()
		proxyProc.Wait()
		e.in("proxy", "nft", "delete", "table", "inet", "torii")
		e.in("proxy", "sh", "-c", "echo 1 > /proc/sys/net/ipv4/ip_forward")
		got := e.client("web", "MODE=tcp", "TO=203.0.113.10:80")
		check(t, "web -> internet, program stopped", got, "error:")
		out, _ := exec.Command("ip", "netns", "exec", e.ns("proxy"), "nft", "list", "counter", "inet", "torii-guard", "forward_dropped").Output()
		if m := regexp.MustCompile(`packets (\d+)`).FindStringSubmatch(string(out)); m == nil || m[1] == "0" {
			t.Errorf("the guard dropped no forwarded packet:\n%s", out)
		}
	})
	_ = time.Second
}
