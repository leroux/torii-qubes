package intercept

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/leroux/torii-qubes/claims"
)

// The guard is packaging/proxy's nftables file and its systemd units: no
// Go loads it. These tests read the files as installed.
const pkg = "../../../packaging/proxy"

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(pkg, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// unitValues returns every value of key in a systemd unit, split on spaces.
func unitValues(unit, key string) []string {
	var out []string
	for _, l := range strings.Split(unit, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), key+"="); ok {
			out = append(out, strings.Fields(v)...)
		}
	}
	return out
}

func has(vs []string, v string) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}

func TestTheGuardLoadsBeforeQubesNetwork(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_JWK2, "guard-before-network")
	g := read(t, "torii-guard.service")
	if !has(unitValues(g, "DefaultDependencies"), "no") {
		t.Error("the guard waits for the default dependencies, so it may come after the network")
	}
	for _, u := range []string{"network-pre.target", "qubes-network.service", "qubes-network-uplink.service", "torii-proxy.service"} {
		if !has(unitValues(g, "Before"), u) {
			t.Errorf("the guard is not ordered before %s", u)
		}
	}
	if !has(unitValues(g, "WantedBy"), "sysinit.target") || !strings.Contains(g, "ExecStart=/usr/sbin/nft -f /usr/lib/torii/torii-guard.nft") {
		t.Error("the guard is not loaded at boot from the installed ruleset")
	}
	dropIn := read(t, "qubes-network-requires-guard.conf")
	if !has(unitValues(dropIn, "Requires"), "torii-guard.service") || !has(unitValues(dropIn, "After"), "torii-guard.service") {
		t.Error("Qubes' network does not require the guard")
	}
	if !strings.Contains(read(t, "nfpm.yaml"), "dst: /usr/lib/systemd/system/qubes-network.service.d/torii-guard.conf") {
		t.Error("the requirement is not installed as qubes-network.service's drop-in")
	}
	p := read(t, "torii-proxy.service")
	if !has(unitValues(p, "Requires"), "torii-guard.service") {
		t.Error("the program runs without the guard")
	}
	if strings.Contains(p, "torii-guard") && strings.Contains(p, "nft -f /usr/lib/torii/torii-guard.nft") {
		t.Error("the program's unit loads the guard; it must not touch it")
	}
}

func TestTheGuardDropsEveryForwardedPacket(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_JWK2, "forward-dropped", "program-stopped")
	nft := read(t, "torii-guard.nft")
	if !regexp.MustCompile(`chain forward \{\s*type filter hook forward priority -400; policy drop;\s*counter name forward_dropped drop\s*\}`).MatchString(nft) {
		t.Errorf("the forward chain does not drop everything at -400:\n%s", nft)
	}
	for _, want := range []string{
		`iifname "vif*" meta nfproto ipv6 counter name ipv6_dropped drop`,
		`meta iif != 0 counter name tripwire log prefix "torii-guard-tripwire " drop`,
		`oifname != "vif*" ip daddr { 10.137.0.0/16, 10.138.0.0/16 } counter name qubes_dropped drop`,
	} {
		if !strings.Contains(nft, want) {
			t.Errorf("the guard lacks %q", want)
		}
	}
	for _, hook := range []string{"prerouting", "forward", "postrouting", "output"} {
		if !regexp.MustCompile(`type filter hook ` + hook + ` priority -400;`).MatchString(nft) {
			t.Errorf("the %s chain is not at priority -400", hook)
		}
	}
	// Nothing of the program names the guard, so nothing it does, stopped,
	// crashed or reloading, changes or removes it; its own rules are a
	// table of their own.
	err := filepath.WalkDir("../..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		if b, _ := os.ReadFile(p); strings.Contains(string(b), "torii-guard") {
			t.Errorf("%s names the guard; the program must leave it alone", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Rules("vif*", 1, 2, 3), "table inet torii\n") || strings.Contains(Rules("vif*", 1, 2, 3), "torii-guard") {
		t.Error("the program's rules are not a table of their own")
	}
	// The ruleset parses, where nft can check it.
	if path, err := exec.LookPath("nft"); err == nil && os.Geteuid() == 0 {
		if out, err := exec.Command(path, "-c", "-f", filepath.Join(pkg, "torii-guard.nft")).CombinedOutput(); err != nil {
			t.Errorf("nft -c: %v: %s", err, out)
		}
	}
}
