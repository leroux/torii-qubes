package policy

import (
	"github.com/leroux/torii-qubes/claims"
	"net/netip"
	"testing"

	"github.com/leroux/torii-qubes/proxy/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte(`{
	  "proxy_qube": "sys-torii", "state_dir": "/s",
	  "workloads": {
	    "airgap":  {"link": "qrexec", "identity": {"tags": ["tag:server"]}, "inbound": [{"port": 443}, {"port": 8443, "target": 443}]},
	    "job":     {"link": "qrexec", "identity": {"tags": ["tag:job"]}, "tailnet_outbound": true, "socks": true,
	                "internet": "allowlist", "internet_allow": ["pypi.org", "*.github.com"],
	                "forwards": [{"name": "db", "to": "store:8000"}]},
	    "browser": {"link": "routed", "address": "10.137.0.20", "identity": {"tags": ["tag:browser"]}, "tailnet_outbound": true},
	    "web":     {"link": "routed", "address": "10.137.0.21", "internet": "full"},
	    "work":    {"link": "routed", "address": "10.137.0.22", "identity": {"login": true}, "tailnet_outbound": true, "internet": "full", "egress": "vpn"},
	    "shop":    {"link": "routed", "address": "10.137.0.23", "internet": "full", "egress": "vpn"},
	    "sys-torii": {"link": "none", "internet": "full", "socks": true}
	  },
	  "egresses": {"vpn": {"hostname": "m1-egress-vpn", "tags": ["tag:m1-egress"], "exits": ["exit-1", "exit-2"]}},
	  "unlisted": {"internet": "allowlist", "internet_allow": ["example.com"], "egress": "vpn"}}`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDecideOutbound(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_PXX9, "unknown-source-refused", "wrong-entry-refused", "undeclared-socks-refused")
	c := testConfig(t)
	ip := netip.MustParseAddr
	cases := []struct {
		name   string
		o      Outbound
		want   Verdict
		reason Reason
		path   Path
	}{
		{"unknown workload", Outbound{Workload: "nope", Entry: EntrySOCKS}, Refuse, ReasonUnknownWorkload, ""},
		{"routed qube cannot use socks", Outbound{Workload: "browser", Entry: EntrySOCKS, Tailnet: true}, Refuse, ReasonWrongEntry, ""},
		{"qrexec qube is never intercepted", Outbound{Workload: "job", Entry: EntryIntercept, Tailnet: true}, Refuse, ReasonWrongEntry, ""},
		{"socks not declared", Outbound{Workload: "airgap", Entry: EntrySOCKS, Host: "pypi.org"}, Refuse, ReasonNoSOCKS, ""},
		{"airgap forward has no outbound", Outbound{Workload: "airgap", Entry: EntryForward, Tailnet: true}, Refuse, ReasonNoTailnet, ""},
		{"tailnet allowed", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "store", Tailnet: true}, Allow, ReasonTailnetOutbound, PathTailnet},
		{"allowlisted name", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "PyPI.org.", Port: 443}, Allow, ReasonAllowlisted, PathInternet},
		{"wildcard subdomain", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "api.github.com"}, Allow, ReasonAllowlisted, PathInternet},
		{"wildcard is not the apex", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "github.com"}, Refuse, ReasonNotAllowlisted, ""},
		{"exact name is not its subdomains", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "www.pypi.org"}, Refuse, ReasonNotAllowlisted, ""},
		{"lookalike suffix", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "evilpypi.org"}, Refuse, ReasonNotAllowlisted, ""},
		{"raw ip on allowlist", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "151.101.0.223"}, Refuse, ReasonUnresolvedIP, ""},
		{"sni must match too", Outbound{Workload: "job", Entry: EntrySOCKS, Host: "pypi.org", SNI: "evil.com"}, Refuse, ReasonSNINotAllowed, ""},
		{"tailnet only browser, internet", Outbound{Workload: "browser", Entry: EntryIntercept, Addr: ip("1.1.1.1"), Port: 443}, Refuse, ReasonInternetNone, ""},
		{"tailnet only browser, tailnet", Outbound{Workload: "browser", Entry: EntryIntercept, Addr: ip("100.64.0.5"), Port: 443, Tailnet: true}, Allow, ReasonTailnetOutbound, PathTailnet},
		{"cgnat not on tailnet is never internet", Outbound{Workload: "web", Entry: EntryIntercept, Addr: ip("100.64.0.5"), Port: 443}, Refuse, ReasonForbiddenAddress, ""},
		{"no identity, tailnet", Outbound{Workload: "web", Entry: EntryIntercept, Addr: ip("100.64.0.5"), Tailnet: true}, Refuse, ReasonNoTailnet, ""},
		{"full internet", Outbound{Workload: "web", Entry: EntryIntercept, Addr: ip("93.184.216.34"), Port: 443}, Allow, ReasonInternetFull, PathInternet},
		{"loopback forbidden", Outbound{Workload: "web", Entry: EntryIntercept, Addr: ip("127.0.0.1"), Port: 22}, Refuse, ReasonForbiddenAddress, ""},
		{"qubes dns forbidden as tcp dest", Outbound{Workload: "web", Entry: EntryIntercept, Addr: ip("10.139.1.1"), Port: 53}, Refuse, ReasonForbiddenAddress, ""},
		{"exit node path", Outbound{Workload: "work", Entry: EntryIntercept, Addr: ip("93.184.216.34"), Port: 443}, Allow, ReasonInternetFull, PathExit},
		{"the proxy qube's own socks", Outbound{Workload: "sys-torii", Entry: EntrySOCKS, Host: "example.com"}, Allow, ReasonInternetFull, PathInternet},
		{"the proxy qube is never intercepted", Outbound{Workload: "sys-torii", Entry: EntryIntercept, Addr: ip("1.1.1.1")}, Refuse, ReasonWrongEntry, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DecideOutbound(c, tc.o)
			if d.Verdict != tc.want || d.Reason != tc.reason || d.Path != tc.path {
				t.Fatalf("got %+v, want %s %s %s", d, tc.want, tc.reason, tc.path)
			}
		})
	}
	if d := DecideOutbound(c, Outbound{Workload: "work", Entry: EntryIntercept, Addr: ip("8.8.8.8")}); d.Identity != "m1-egress-vpn" {
		t.Errorf("exit path carries the egress's identity, got %q", d.Identity)
	}
	// The workload's own identity still carries its tailnet traffic.
	if d := DecideOutbound(c, Outbound{Workload: "work", Entry: EntryIntercept, Addr: ip("100.64.0.5"), Tailnet: true}); d.Path != PathTailnet || d.Identity != "work" {
		t.Errorf("tailnet path: %+v", d)
	}
	if d := DecideOutbound(c, Outbound{Workload: "job", Entry: EntrySOCKS, Host: "pypi.org"}); d.Identity != "" {
		t.Errorf("direct internet uses no identity, got %q", d.Identity)
	}
}

func TestAllowlistByResolvedName(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_2R9T, "resolved-name-allows", "unresolved-address-refused")
	c, err := config.Parse([]byte(`{"proxy_qube":"p","state_dir":"/s","workloads":{
	  "r": {"link":"routed","address":"10.137.0.9","internet":"allowlist","internet_allow":["pypi.org"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	a := netip.MustParseAddr("151.101.0.223")
	o := Outbound{Workload: "r", Entry: EntryIntercept, Addr: a, Port: 443}
	if d := DecideOutbound(c, o); d.Reason != ReasonUnresolvedIP {
		t.Errorf("unresolved ip: %+v", d)
	}
	o.ResolvedFrom = []string{"files.example.net"}
	if d := DecideOutbound(c, o); d.Reason != ReasonNotAllowlisted {
		t.Errorf("resolved from another name: %+v", d)
	}
	o.ResolvedFrom = []string{"files.example.net", "pypi.org"}
	if d := DecideOutbound(c, o); d.Verdict != Allow || d.Host != "pypi.org" {
		t.Errorf("resolved from allowed name: %+v", d)
	}
	o.SNI = "files.example.net"
	if d := DecideOutbound(c, o); d.Reason != ReasonSNINotAllowed {
		t.Errorf("shared address, other server name: %+v", d)
	}
}

func TestDecideNameAndInbound(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_T7GG, "listed-port-mapped", "unlisted-inbound-refused")
	c := testConfig(t)
	if d := DecideName(c, "browser", "store.tail.ts.net", true); d.Verdict != Allow || d.Path != PathTailnet {
		t.Errorf("%+v", d)
	}
	if d := DecideName(c, "browser", "example.com", false); d.Reason != ReasonInternetNone {
		t.Errorf("%+v", d)
	}
	if d := DecideName(c, "web", "store.tail.ts.net", true); d.Reason != ReasonNoTailnet {
		t.Errorf("%+v", d)
	}
	if d := DecideName(c, "work", "example.com", false); d.Path != PathExit {
		t.Errorf("%+v", d)
	}
	if d, tp := DecideInbound(c, "airgap", 8443); d.Verdict != Allow || tp != 443 {
		t.Errorf("%+v %d", d, tp)
	}
	if d, _ := DecideInbound(c, "airgap", 22); d.Reason != ReasonNoInboundPort {
		t.Errorf("%+v", d)
	}
	if d, _ := DecideInbound(c, "web", 80); d.Reason != ReasonUnknownWorkload {
		t.Errorf("no identity: %+v", d)
	}
	if to, ok := ForwardTarget(c, "job", "db"); !ok || to != "store:8000" {
		t.Errorf("forward %q %v", to, ok)
	}
}

// An egress identity is internet-only: a workload without an identity of
// its own never reaches the tailnet through its egress, whatever it dials.
func TestEgressNeverCarriesTheTailnet(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_PXX9, "unlisted-no-tailnet")
	c := testConfig(t)
	for _, wl := range []string{"shop", "unlisted:10.137.0.99"} {
		for _, a := range []string{"100.64.0.5", "100.100.100.100", "fd7a:115c:a1e0::1"} {
			d := DecideOutbound(c, Outbound{Workload: wl, Entry: EntryIntercept, Addr: netip.MustParseAddr(a), Port: 443})
			if d.Verdict != Refuse {
				t.Errorf("%s -> %s: %+v", wl, a, d)
			}
		}
	}
}

func TestUnlistedSources(t *testing.T) {
	c := testConfig(t)
	u := "unlisted:10.137.0.99"
	ip := netip.MustParseAddr
	if d := DecideOutbound(c, Outbound{Workload: u, Entry: EntryIntercept, Addr: ip("93.184.216.34"), Port: 443,
		ResolvedFrom: []string{"example.com"}}); d.Verdict != Allow || d.Path != PathExit || d.Identity != "m1-egress-vpn" {
		t.Errorf("allowlisted: %+v", d)
	}
	if d := DecideOutbound(c, Outbound{Workload: u, Entry: EntryIntercept, Addr: ip("1.1.1.1"), Port: 443,
		ResolvedFrom: []string{"other.org"}}); d.Verdict != Refuse {
		t.Errorf("not allowlisted: %+v", d)
	}
	if d := DecideOutbound(c, Outbound{Workload: u, Entry: EntrySOCKS, Host: "example.com", Port: 443}); d.Reason != ReasonWrongEntry {
		t.Errorf("an unlisted source is routed: %+v", d)
	}
	if d := DecideName(c, u, "example.com", false); d.Verdict != Allow || d.Path != PathExit {
		t.Errorf("name: %+v", d)
	}
	c.Unlisted = nil
	if d := DecideOutbound(c, Outbound{Workload: u, Entry: EntryIntercept, Addr: ip("93.184.216.34")}); d.Reason != ReasonUnknownWorkload {
		t.Errorf("no unlisted: %+v", d)
	}
}

// Match is exact for a name and strict about a wildcard: neither widens
// what the entry says.
func TestMatch(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_2XS6, "exact-name-only", "wildcard-not-parent")
	for _, c := range []struct {
		allow, host string
		want        bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "EXAMPLE.com.", true},
		{"example.com", "www.example.com", false},
		{"example.com", "badexample.com", false},
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "aexample.com", false},
	} {
		if got := Match([]string{c.allow}, c.host); got != c.want {
			t.Errorf("Match(%s, %s) = %v", c.allow, c.host, got)
		}
	}
}
