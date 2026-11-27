//go:build linux

package intercept

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/leroux/torii-qubes/claims"
)

// chain returns the body of the named chain in an nftables ruleset.
func chain(t *testing.T, rules, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)chain ` + name + ` \{(.*?)\n\t\}`).FindStringSubmatch(rules)
	if m == nil {
		t.Fatalf("no chain %s in:\n%s", name, rules)
	}
	return m[1]
}

func TestRulesHandEverythingToTheProgram(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_B6Z6, "all-redirected", "input-closed")
	r := Rules("vif*", 7000, 7053, 7100)
	pre := chain(t, r, "prerouting")
	for _, want := range []string{
		`iifname "vif*" udp dport 53 redirect to :7053`,
		`iifname "vif*" tcp dport 53 redirect to :7053`,
		`iifname "vif*" meta l4proto tcp redirect to :7000`,
	} {
		if !strings.Contains(pre, want) {
			t.Errorf("prerouting lacks %q", want)
		}
	}
	if !strings.Contains(chain(t, r, "mangle"), `iifname "vif*" meta nfproto ipv4 udp dport != 53 meta mark set 0x`) ||
		!strings.Contains(chain(t, r, "mangle"), "tproxy ip to :7100") {
		t.Errorf("other IPv4 UDP is not handed to the program:\n%s", chain(t, r, "mangle"))
	}
	in := strings.Split(strings.TrimSpace(chain(t, r, "input")), "\n")
	for i := range in {
		in[i] = strings.TrimSpace(in[i])
	}
	if last := in[len(in)-1]; last != `iifname "vif*" counter drop` {
		t.Errorf("input from the workloads ends %q, not in a drop", last)
	}
	for _, l := range in[1 : len(in)-1] {
		if !strings.HasSuffix(l, "accept") {
			t.Errorf("input rule %q is not an accept", l)
			continue
		}
		ok := strings.Contains(l, "ct state established,related") || strings.Contains(l, "dport 7000") ||
			strings.Contains(l, "dport 7053") || strings.Contains(l, "meta mark") || strings.Contains(l, "icmp")
		if !ok {
			t.Errorf("input accepts something other than the program's ports, what was handed to it, ICMP and established: %q", l)
		}
	}
	if !strings.Contains(chain(t, r, "forward"), "policy drop;") {
		t.Errorf("forward chain does not drop:\n%s", chain(t, r, "forward"))
	}
}

func TestForwardingIsTurnedOff(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_B6Z6, "forwarding-off")
	dir := t.TempDir()
	v4, v6 := filepath.Join(dir, "ip_forward"), filepath.Join(dir, "forwarding")
	for _, p := range []string{v4, v6} {
		if err := os.WriteFile(p, []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := forwardingOff([]string{v4, v6, filepath.Join(dir, "absent")}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{v4, v6} {
		if b, _ := os.ReadFile(p); string(b) != "0\n" {
			t.Errorf("%s = %q after the program started", filepath.Base(p), b)
		}
	}
	if len(forwardingPaths) != 2 || forwardingPaths[0] != "/proc/sys/net/ipv4/ip_forward" || forwardingPaths[1] != "/proc/sys/net/ipv6/conf/all/forwarding" {
		t.Errorf("forwarding switches: %v", forwardingPaths)
	}
	if !strings.Contains(chain(t, Rules("vif*", 1, 2, 3), "forward"), "policy drop;") {
		t.Error("a forwarded packet is not dropped")
	}
}
