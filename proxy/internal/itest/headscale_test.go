package itest

// A local Headscale as the coordination server: real tags, real grants,
// real registration. Two machines' proxy programs (alice-proxy and bob-proxy)
// and two people's laptops (alice and bob) share one tailnet.
//
// Needs the headscale binary: $HEADSCALE, or headscale on PATH.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
	"tailscale.com/net/netns"
	"tailscale.com/tstest/integration"
	"tailscale.com/types/logger"

	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/engine"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

// The grants under test, as a small team would write them.
const teamPolicy = `{
  "groups": {"group:team": ["alice@", "bob@"]},
  "tagOwners": {
    "tag:server": ["alice@"],
    "tag:job":    ["alice@"],
    "tag:db":     ["bob@"]
  },
  "grants": [
    // Everyone on the team reaches the servers' web ports.
    {"src": ["group:team"], "dst": ["tag:server"], "ip": ["tcp:443"]},
    // Only alice reaches the database.
    {"src": ["alice@"], "dst": ["tag:db"], "ip": ["tcp:5432"]},
    // Jobs reach the database, not the servers.
    {"src": ["tag:job"], "dst": ["tag:db"], "ip": ["tcp:5432"]}
  ]
}`

type headscale struct {
	t   *testing.T
	bin string
	cfg string
	url string
	cmd *exec.Cmd
	dir string
}

func startHeadscale(t *testing.T) *headscale {
	t.Helper()
	bin := os.Getenv("HEADSCALE")
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("headscale"); err != nil {
			t.Skip("no headscale binary ($HEADSCALE or PATH)")
		}
	}
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	derp := integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1")
	n := derp.Regions[1].Nodes[0]
	dir := t.TempDir()
	derpYAML := fmt.Sprintf(`regions:
  900:
    regionid: 900
    regioncode: test
    regionname: test
    nodes:
      - name: 900a
        regionid: 900
        hostname: 127.0.0.1
        ipv4: 127.0.0.1
        ipv6: none
        stunport: %d
        derpport: %d
        insecurefortests: true
        stuntestip: 127.0.0.1
`, n.STUNPort, n.DERPPort)
	os.WriteFile(filepath.Join(dir, "derp.yaml"), []byte(derpYAML), 0o644)
	os.WriteFile(filepath.Join(dir, "policy.hujson"), []byte(teamPolicy), 0o644)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	cfg := fmt.Sprintf(`server_url: http://%[1]s
listen_addr: %[1]s
metrics_listen_addr: 127.0.0.1:0
grpc_listen_addr: 127.0.0.1:0
noise:
  private_key_path: %[2]s/noise.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
derp:
  server:
    enabled: false
  urls: []
  paths: [%[2]s/derp.yaml]
  auto_update_enabled: false
disable_check_updates: true
database:
  type: sqlite
  sqlite:
    path: %[2]s/db.sqlite
log:
  level: warn
policy:
  mode: file
  path: %[2]s/policy.hujson
dns:
  magic_dns: true
  base_domain: tail.test
  override_local_dns: false
  nameservers:
    global: []
unix_socket: %[2]s/hs.sock
unix_socket_permission: "0770"
logtail:
  enabled: false
`, addr, dir)
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(cfg), 0o644)
	h := &headscale{t: t, bin: bin, cfg: cfgPath, url: "http://" + addr, dir: dir}
	h.cmd = exec.Command(bin, "-c", cfgPath, "serve")
	logf, _ := os.Create(filepath.Join(dir, "headscale.log"))
	h.cmd.Stdout, h.cmd.Stderr = logf, logf
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.cmd.Process.Kill()
		h.cmd.Wait()
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(dir, "headscale.log"))
			t.Logf("headscale log:\n%s", b)
		}
	})
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			if _, err := os.Stat(filepath.Join(dir, "hs.sock")); err == nil {
				return h
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("headscale did not start")
	return nil
}

// run runs a headscale CLI command and returns its JSON output.
func (h *headscale) run(args ...string) ([]byte, error) {
	out, err := exec.Command(h.bin, append([]string{"-c", h.cfg, "--force", "-o", "json"}, args...)...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("headscale %v: %v: %s", args, err, out)
	}
	return out, nil
}

func (h *headscale) mustRun(args ...string) []byte {
	h.t.Helper()
	out, err := h.run(args...)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *headscale) user(name string) uint64 {
	h.t.Helper()
	var u struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(h.mustRun("users", "create", name), &u); err != nil {
		h.t.Fatal(err)
	}
	return u.ID
}

// key makes a one-time pre-authenticated key: for a person (user) or,
// with tags, for tagged identities.
func (h *headscale) key(user uint64, tags ...string) string {
	h.t.Helper()
	args := []string{"preauthkeys", "create", "--expiration", "1h"}
	if user != 0 {
		args = append(args, "--user", fmt.Sprint(user))
	}
	if len(tags) > 0 {
		args = append(args, "--tags", strings.Join(tags, ","))
	}
	var k struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(h.mustRun(args...), &k); err != nil {
		h.t.Fatal(err)
	}
	return k.Key
}

type hsNode struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
	User struct {
		Name string `json:"name"`
	} `json:"user"`
}

func (h *headscale) nodes() []hsNode {
	var n []hsNode
	json.Unmarshal(h.mustRun("nodes", "list"), &n)
	return n
}

func keyFile(t *testing.T, key string) string {
	p := filepath.Join(t.TempDir(), "authkey")
	os.WriteFile(p, []byte(key+"\n"), 0o600)
	return p
}

// proxyMachine starts one machine's proxy program against headscale.
func proxyMachine(t *testing.T, ctx context.Context, h *headscale, cfg *config.Config, q engine.QubeDialer) (*engine.Engine, map[string]tailnet.Identity, *events.Log) {
	t.Helper()
	cfg.ControlURL = h.url
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	ids := map[string]tailnet.Identity{}
	for _, n := range cfg.WorkloadNames() {
		w := cfg.Workloads[n]
		if w.Identity == nil {
			continue
		}
		key := ""
		if w.Identity.AuthKeyFile != "" {
			b, _ := os.ReadFile(w.Identity.AuthKeyFile)
			key = strings.TrimSpace(string(b))
		}
		ids[n] = tailnet.NewTSNet(tailnet.TSNetOptions{
			Hostname: w.Identity.Hostname, Dir: filepath.Join(cfg.StateDir, n), ControlURL: h.url,
			Tags: w.Identity.Tags, AuthKey: key, Logf: debugLogf(t),
		})
	}
	log := &events.Log{}
	eng := engine.New(engine.Deps{Config: cfg, Identities: ids, Qubes: q, Log: log, DialTimeout: 5 * time.Second})
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng, ids, log
}

func TestHeadscaleLab(t *testing.T) {
	if testing.Short() {
		t.Skip("starts headscale")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	h := startHeadscale(t)
	m := h.user("alice")
	v := h.user("bob")

	// Services inside qubes without a network card.
	q := &qubes{m: map[string]string{}}
	for _, s := range []struct {
		qube, greeting string
		port           int
	}{{"infra", "infra", 443}, {"bob-db", "bob-db", 5432}} {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		serveGreeting(t, ln, s.greeting)
		q.m[fmt.Sprintf("%s:%d", s.qube, s.port)] = ln.Addr().String()
	}

	// Machine M: a server and a job, tagged keys pasted into the proxy qube.
	mEng, mIDs, mLog := proxyMachine(t, ctx, h, &config.Config{Version: "m1", ProxyQube: "alice-proxy", StateDir: t.TempDir(),
		Workloads: map[string]*config.Workload{
			"infra": {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:server"},
				AuthKeyFile: keyFile(t, h.key(m, "tag:server"))}, Inbound: []config.Inbound{{Port: 443}, {Port: 22}}},
			"job": {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:job"},
				AuthKeyFile: keyFile(t, h.key(m, "tag:job"))}, TailnetOutbound: true, SOCKS: true},
		}}, q)
	// Machine V: a database.
	_, vIDs, vLog := proxyMachine(t, ctx, h, &config.Config{Version: "v1", ProxyQube: "bob-proxy", StateDir: t.TempDir(),
		Workloads: map[string]*config.Workload{
			"bob-db": {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:db"},
				AuthKeyFile: keyFile(t, h.key(v, "tag:db"))}, Inbound: []config.Inbound{{Port: 5432}}},
		}}, q)
	for n, id := range mIDs {
		if err := id.AwaitRunning(ctx); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	for n, id := range vIDs {
		if err := id.AwaitRunning(ctx); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	// The people's laptops: personal (login) identities.
	laptops := map[string]*tailnet.TSNet{}
	for name, uid := range map[string]uint64{"laptop-alice": m, "laptop-bob": v} {
		id := tailnet.NewTSNet(tailnet.TSNetOptions{Hostname: name, Dir: filepath.Join(t.TempDir(), name), ControlURL: h.url,
			AuthKey: h.key(uid)})
		up(t, ctx, id)
		laptops[name] = id
	}

	ipOf := func(id tailnet.Identity) netip.Addr { return netip.MustParseAddr(id.Status(ctx).IPs[0]) }
	// reach dials from one identity, retrying while netmaps converge, and
	// returns the greeting or "".
	reach := func(from tailnet.Identity, to tailnet.Identity, port uint16, tries int) string {
		for i := 0; i < tries; i++ {
			dctx, dc := context.WithTimeout(ctx, 2*time.Second)
			c, err := from.Dial(dctx, netip.AddrPortFrom(ipOf(to), port))
			dc()
			if err == nil {
				c.SetReadDeadline(time.Now().Add(3 * time.Second))
				line, err := bufio.NewReader(c).ReadString('\n')
				c.Close()
				if err == nil {
					return strings.TrimSpace(line)
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		return ""
	}

	t.Run("identities are tagged as their keys say", func(t *testing.T) {
		tags := map[string]string{}
		for _, n := range h.nodes() {
			tags[n.Name] = strings.Join(n.Tags, ",")
		}
		for name, want := range map[string]string{"infra": "tag:server", "job": "tag:job", "bob-db": "tag:db", "laptop-alice": ""} {
			if tags[name] != want {
				t.Errorf("%s tags %q, want %q (all: %v)", name, tags[name], want, tags)
			}
		}
	})

	t.Run("status shows the assigned tags and no mismatch", func(t *testing.T) {
		st := mEng.Status(ctx).Workloads["infra"].Identity
		if strings.Join(st.Tags, ",") != "tag:server" || st.Err != "" {
			t.Errorf("%+v", st)
		}
	})

	t.Run("both people reach the server's granted port", func(t *testing.T) {
		for _, l := range []string{"laptop-alice", "laptop-bob"} {
			if got := reach(laptops[l], mIDs["infra"], 443, 40); got != "infra" {
				t.Errorf("%s -> infra:443: %q", l, got)
			}
		}
		ev := waitEvent(t, mLog, func(e events.Event) bool { return e.Direction == events.In && strings.Contains(e.Source, "laptop-bob") })
		if !strings.Contains(ev.Source, "(bob@") && !strings.Contains(ev.Source, "(bob)") {
			t.Logf("inbound source: %q", ev.Source)
		}
	})

	t.Run("a declared port without a grant stays closed", func(t *testing.T) {
		// 22 is declared inbound in the file, but no grant allows it.
		if got := reach(laptops["laptop-alice"], mIDs["infra"], 22, 3); got != "" {
			t.Errorf("port 22 reachable: %q", got)
		}
		for _, e := range mLog.Recent("infra") {
			if strings.HasSuffix(e.Dest, ":22") {
				t.Errorf("a connection to 22 reached the program; grants should stop it first: %+v", e)
			}
		}
	})

	t.Run("only alice reaches the database, across machines", func(t *testing.T) {
		if got := reach(laptops["laptop-alice"], vIDs["bob-db"], 5432, 40); got != "bob-db" {
			t.Errorf("laptop-alice -> bob-db: %q", got)
		}
		if got := reach(laptops["laptop-bob"], vIDs["bob-db"], 5432, 3); got != "" {
			t.Errorf("laptop-bob -> bob-db: %q", got)
		}
		waitEvent(t, vLog, func(e events.Event) bool { return e.Direction == events.In && strings.Contains(e.Source, "laptop-alice") })
	})

	t.Run("a job reaches the database by name over socks, not the server", func(t *testing.T) {
		d, _ := proxy.SOCKS5("tcp", "qrexec", nil, pipeDialer(func() net.Conn {
			a, b := net.Pipe()
			go mEng.HandleQrexec(ctx, "job", "socks", b)
			return a
		}))
		var got string
		for i := 0; i < 40 && got == ""; i++ {
			c, err := d.(proxy.ContextDialer).DialContext(ctx, "tcp", "bob-db.tail.test:5432")
			if err == nil {
				c.SetReadDeadline(time.Now().Add(3 * time.Second))
				line, _ := bufio.NewReader(c).ReadString('\n')
				got = strings.TrimSpace(line)
				c.Close()
			} else {
				time.Sleep(200 * time.Millisecond)
			}
		}
		if got != "bob-db" {
			t.Fatalf("job -> bob-db: %q", got)
		}
		if got := reach(mIDs["job"], mIDs["infra"], 443, 3); got != "" {
			t.Errorf("job -> infra:443: %q", got)
		}
	})

	t.Run("sign-in by link: the tag owner approves", func(t *testing.T) {
		cfg := &config.Config{ProxyQube: "alice-proxy", StateDir: t.TempDir(), Workloads: map[string]*config.Workload{
			"new-svc":  {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:server"}}, Inbound: []config.Inbound{{Port: 443}}},
			"rogue-db": {Link: config.LinkQrexec, Identity: &config.Identity{Tags: []string{"tag:db"}}},
		}}
		q2 := &qubes{m: map[string]string{"new-svc:443": q.m["infra:443"]}}
		eng, ids, _ := proxyMachine(t, ctx, h, cfg, q2)
		authID := func(w string) string {
			var u string
			waitFor(t, 20*time.Second, func() bool {
				u = eng.Status(ctx).Workloads[w].Identity.AuthURL
				return u != ""
			})
			t.Logf("%s sign-in link: %s", w, u)
			return regexp.MustCompile(`[^/]+$`).FindString(u)
		}
		// m owns tag:server: approving makes a tagged node.
		h.mustRun("auth", "register", "--user", "alice", "--auth-id", authID("new-svc"))
		if err := ids["new-svc"].AwaitRunning(ctx); err != nil {
			t.Fatal(err)
		}
		for _, n := range h.nodes() {
			if n.Name == "new-svc" && strings.Join(n.Tags, ",") != "tag:server" {
				t.Errorf("new-svc tags %v owner %q", n.Tags, n.User.Name)
			}
		}
		if got := reach(laptops["laptop-bob"], ids["new-svc"], 443, 40); got != "infra" {
			t.Errorf("laptop-bob -> new-svc: %q", got)
		}
		// m does not own tag:db: the approval is refused.
		if out, err := h.run("auth", "register", "--user", "alice", "--auth-id", authID("rogue-db")); err == nil {
			t.Errorf("m approved a tag:db identity: %s", out)
		} else {
			t.Logf("refused as expected: %v", err)
		}
		time.Sleep(time.Second)
		if st := eng.Status(ctx).Workloads["rogue-db"].Identity.State; st == "Running" {
			t.Error("rogue-db is running")
		}
	})
}
