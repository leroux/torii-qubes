//go:build linux

package intercept

import (
	"fmt"
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
	var in []string
	for _, l := range strings.Split(strings.TrimSpace(chain(t, r, "input")), "\n") {
		in = append(in, strings.TrimSpace(l))
	}
	want := []string{
		"type filter hook input priority filter - 10; policy accept;",
		`iifname "vif*" ct state established,related accept`,
		`iifname "vif*" tcp dport 7000 accept`,
		`iifname "vif*" meta l4proto { tcp, udp } th dport 7053 accept`,
		`iifname "vif*" meta mark 0x` + strings.TrimPrefix(fmt.Sprintf("%#x", TProxyMark), "0x") + ` meta l4proto udp accept`,
		`iifname "vif*" meta l4proto { icmp, ipv6-icmp } accept`,
		`iifname "vif*" counter drop`,
	}
	if strings.Join(in, "\n") != strings.Join(want, "\n") {
		t.Errorf("input chain:\n%s\nwant:\n%s", strings.Join(in, "\n"), strings.Join(want, "\n"))
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
