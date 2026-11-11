package itest

// An egress against the real Tailscale service: two real exit nodes
// (tailscaled in userspace mode on this machine), real WireGuard over
// Tailscale's relays, real internet behind them. Opt in with
//
//	TORII_TS_API_KEY_FILE=~/tskey  a Tailscale API access token (tskey-api-...)
//	TORII_TS_EGRESS_TAG=tag:x      a tag the policy grants autogroup:internet
//	TORII_TS_EXIT=name-or-IP       optional: an exit node already on the tailnet,
//	                               used as the second (its routes approved by the
//	                               API; it is never stopped, so the last part,
//	                               every exit node down, is skipped)
//
// It never changes the tailnet policy. It makes single-use, ephemeral,
// pre-authorized auth keys (one hour), approves the exit routes of the two
// exit nodes it starts, and deletes every device and key it made.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/engine"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

type tsAPI struct {
	t     *testing.T
	token string
}

func (a *tsAPI) do(method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "https://api.tailscale.com/api/v2"+path, r)
	req.SetBasicAuth(a.token, "")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// authKey makes a single-use, ephemeral, pre-authorized key and deletes it
// at the end.
func (a *tsAPI) authKey(tags ...string) string {
	a.t.Helper()
	create := map[string]any{"reusable": false, "ephemeral": true, "preauthorized": true}
	if len(tags) > 0 {
		create["tags"] = tags
	}
	var k struct{ ID, Key string }
	if err := a.do("POST", "/tailnet/-/keys", map[string]any{
		"capabilities":  map[string]any{"devices": map[string]any{"create": create}},
		"expirySeconds": 3600, "description": "torii itest",
	}, &k); err != nil {
		a.t.Fatal(err)
	}
	a.t.Cleanup(func() { a.do("DELETE", "/tailnet/-/keys/"+k.ID, nil, nil) })
	return k.Key
}

type tsDevice struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
}

func (a *tsAPI) devices(prefix string) []tsDevice {
	var d struct{ Devices []tsDevice }
	if err := a.do("GET", "/tailnet/-/devices", nil, &d); err != nil {
		a.t.Log(err)
	}
	var out []tsDevice
	for _, x := range d.Devices {
		if strings.HasPrefix(x.Hostname, prefix) {
			out = append(out, x)
		}
	}
	return out
}

// tailscaled is a real exit node: tailscaled in userspace mode, its own
// state folder and socket.
type tailscaled struct {
	t    *testing.T
	dir  string
	host string
	cmd  *exec.Cmd
}

func (d *tailscaled) start() {
	d.t.Helper()
	logf, _ := os.OpenFile(filepath.Join(d.dir, "tailscaled.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	d.cmd = exec.Command("tailscaled", "--tun=userspace-networking", "--statedir="+d.dir,
		"--socket="+filepath.Join(d.dir, "sock"), "--port=0")
	d.cmd.Stdout, d.cmd.Stderr = logf, logf
	if err := d.cmd.Start(); err != nil {
		d.t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(d.dir, "sock")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.t.Fatal("tailscaled did not start")
}

func (d *tailscaled) cli(args ...string) error {
	out, err := exec.Command("tailscale", append([]string{"--socket=" + filepath.Join(d.dir, "sock")}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale %s: %v: %s", args[0], err, out)
	}
	return nil
}

// kill stops it the hard way: no logout, the control server only learns
// it is gone when its connection drops.
func (d *tailscaled) kill() {
	if d.cmd != nil && d.cmd.Process != nil {
		d.cmd.Process.Kill()
		d.cmd.Wait()
		d.cmd = nil
	}
}

func TestTailscaleEgress(t *testing.T) {
	keyFile, tag := os.Getenv("TORII_TS_API_KEY_FILE"), os.Getenv("TORII_TS_EGRESS_TAG")
	if keyFile == "" || tag == "" {
		t.Skip("set TORII_TS_API_KEY_FILE and TORII_TS_EGRESS_TAG to run against Tailscale")
	}
	for _, bin := range []string{"tailscaled", "tailscale"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("needs " + bin)
		}
	}
	tok, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	api := &tsAPI{t: t, token: strings.TrimSpace(string(tok))}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	rb := make([]byte, 3)
	rand.Read(rb)
	prefix := "torii-it-" + hex.EncodeToString(rb) + "-"
	t.Cleanup(func() { // whatever is left, gone from the tailnet
		for _, d := range api.devices(prefix) {
			api.do("DELETE", "/device/"+d.ID, nil, nil)
		}
	})

	// Two exit nodes, their routes approved by the API.
	exits := map[string]*tailscaled{}
	var exitNames []string
	second := os.Getenv("TORII_TS_EXIT")
	names := []string{"exit-1", "exit-2"}
	if second != "" {
		names = names[:1]
	}
	for _, n := range names {
		d := &tailscaled{t: t, dir: t.TempDir(), host: prefix + n}
		d.start()
		t.Cleanup(func() { d.cli("logout"); d.kill() })
		if err := d.cli("up", "--authkey="+api.authKey(), "--hostname="+d.host, "--advertise-exit-node", "--accept-dns=false", "--timeout=60s"); err != nil {
			t.Fatal(err)
		}
		exits[n], exitNames = d, append(exitNames, d.host)
	}
	for _, d := range exits {
		var dev []tsDevice
		waitFor(t, 30*time.Second, func() bool { dev = api.devices(d.host); return len(dev) == 1 })
		if err := api.do("POST", "/device/"+dev[0].ID+"/routes", map[string]any{"routes": []string{"0.0.0.0/0", "::/0"}}, nil); err != nil {
			t.Fatal(err)
		}
	}

	if second != "" {
		approveExit(t, api, second)
		exitNames = append(exitNames, second)
	}
	e1, e2 := prefix+"exit-1", prefix+"exit-2"
	if second != "" {
		e2 = second
	}

	// "The internet": a real host, reached only through an exit node.
	var inetIP netip.Addr
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", "example.com")
	if err != nil || len(ips) == 0 {
		t.Skipf("no internet here: %v", err)
	}
	inetIP = ips[0]
	inetAddr := netip.AddrPortFrom(inetIP, 80)

	cfg := &config.Config{
		Version: "it", ProxyQube: "sys-torii", StateDir: t.TempDir(),
		Workloads: map[string]*config.Workload{
			// No identity of its own: only the egress's.
			"work": {Link: config.LinkRouted, Address: "10.137.0.22", Internet: config.InternetFull, Egress: "vpn"},
		},
		Egresses: map[string]*config.Egress{"vpn": {Hostname: prefix + "egress-vpn", Tags: []string{tag},
			Exits: exitNames, FailbackAfter: 20}},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	eid := tailnet.NewTSNet(tailnet.TSNetOptions{Hostname: prefix + "egress-vpn", Dir: filepath.Join(t.TempDir(), "egress"),
		Tags: []string{tag}, AuthKey: api.authKey(tag), Ephemeral: true, Logf: debugLogf(t)})
	ids := map[string]tailnet.Identity{config.EgressIdentityKey("vpn"): eid}
	direct := &countingDialer{}
	log := &events.Log{}
	eng := engine.New(engine.Deps{Config: cfg, Identities: ids, Internet: direct, Log: log, DialTimeout: 10 * time.Second})
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if err := eid.AwaitRunning(ctx); err != nil {
		t.Fatal(err)
	}

	status := func() string {
		st := eng.Status(ctx).Egresses["vpn"]
		var b strings.Builder
		fmt.Fprintf(&b, "active %q usable %v err %q;", strings.TrimPrefix(st.Active, prefix), st.Usable, st.Error)
		for _, x := range st.Exits {
			fmt.Fprintf(&b, " %s healthy=%v %s;", strings.TrimPrefix(x.Name, prefix), x.Healthy, x.Why)
		}
		return b.String()
	}
	waitActive := func(want string, d time.Duration) time.Duration {
		t.Helper()
		start := time.Now()
		deadline := start.Add(d)
		for time.Now().Before(deadline) {
			st := eng.Status(ctx).Egresses["vpn"]
			if st.Usable && st.Active == want {
				t.Logf("%s active after %v: %s", want, time.Since(start).Round(time.Second), status())
				return time.Since(start)
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("%s not active after %v: %s", want, d, status())
		return 0
	}
	// fetch makes one connection as work would, intercepted on its way out.
	port := 40000
	fetch := func() (string, error) {
		port++
		a, b := net.Pipe()
		defer a.Close()
		go eng.HandleIntercepted(ctx, b, netip.AddrPortFrom(netip.MustParseAddr("10.137.0.22"), uint16(port)), inetAddr)
		a.SetDeadline(time.Now().Add(20 * time.Second))
		fmt.Fprintf(a, "HEAD / HTTP/1.0\r\nHost: example.com\r\n\r\n")
		s, err := bufio.NewReader(a).ReadString('\n')
		return strings.TrimSpace(s), err
	}
	through := func(what string) {
		t.Helper()
		var got string
		var err error
		for i := 0; i < 5; i++ {
			if got, err = fetch(); err == nil && strings.HasPrefix(got, "HTTP/1.") {
				t.Logf("%s: %q", what, got)
				return
			}
			time.Sleep(time.Second)
		}
		for _, e := range log.Recent("work") {
			t.Logf("%+v", e)
		}
		t.Fatalf("%s: %q %v; %s", what, got, err, status())
	}

	waitActive(e1, 90*time.Second)
	through("through exit-1")
	if direct.n() != 0 {
		t.Fatal("used the proxy qube's own network")
	}

	// exit-1 dies without logging out.
	exits["exit-1"].kill()
	took := waitActive(e2, 90*time.Second)
	t.Logf("failover took %v", took.Round(time.Second))
	through("through exit-2")

	// exit-1 comes back: not used until healthy for failback-after (20 s).
	exits["exit-1"].start()
	took = waitActive(e1, 3*time.Minute)
	if took < 20*time.Second {
		t.Errorf("failed back after %v, before the hold-down", took)
	}
	through("through exit-1 again")

	if second != "" {
		return // the other exit node is not ours to stop
	}
	// Both die: refused, never direct, the exit node not cleared.
	exits["exit-1"].kill()
	exits["exit-2"].kill()
	waitFor(t, 90*time.Second, func() bool { return !eng.Status(ctx).Egresses["vpn"].Usable })
	t.Logf("none healthy: %s", status())
	start := time.Now()
	if got, err := fetch(); err == nil && got != "" {
		t.Fatalf("got %q with every exit node down", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("refusal took %v: it should not wait on a dial", d)
	}
	if direct.n() != 0 {
		t.Fatal("fell back to the proxy qube's own network")
	}
	if eid.Status(ctx).ExitNode == "" {
		t.Error("the exit node was cleared")
	}
	ev := waitEvent(t, log, func(e events.Event) bool { return e.Workload == "work" && e.Reason == "egress-no-healthy-exit" })
	t.Logf("refused: %+v", ev)
	var kinds []string
	for _, e := range log.Recent("egress:vpn") {
		kinds = append(kinds, string(e.Reason)+" "+strings.TrimPrefix(e.Dest, prefix))
	}
	t.Logf("egress events: %v", kinds)
}

// approveExit enables the exit routes an existing device advertises,
// found by name or tailnet address.
func approveExit(t *testing.T, api *tsAPI, node string) {
	t.Helper()
	var d struct {
		Devices []struct {
			ID         string   `json:"id"`
			Hostname   string   `json:"hostname"`
			Name       string   `json:"name"`
			Addresses  []string `json:"addresses"`
			Advertised []string `json:"advertisedRoutes"`
		}
	}
	if err := api.do("GET", "/tailnet/-/devices?fields=all", nil, &d); err != nil {
		t.Fatal(err)
	}
	for _, x := range d.Devices {
		match := strings.EqualFold(x.Hostname, node) || strings.HasPrefix(x.Name, node+".")
		for _, a := range x.Addresses {
			match = match || a == node
		}
		if !match {
			continue
		}
		if err := api.do("POST", "/device/"+x.ID+"/routes", map[string]any{"routes": x.Advertised}, nil); err != nil {
			t.Fatal(err)
		}
		t.Logf("approved %v on %s", x.Advertised, x.Hostname)
		return
	}
	t.Fatalf("no device %s on the tailnet", node)
}
