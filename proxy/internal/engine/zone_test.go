package engine

import (
	"context"
	"github.com/leroux/torii-qubes/claims"
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
)

// A public name that resolves to the LAN is not dialled there: job's
// allowlist names pypi.org, and its LAN is none.
func TestSOCKSNameRebindingToTheLAN(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_M7G9, "proxy-resolution-filtered")
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "192.168.1.1")
	w.internet.serve(t, "192.168.1.1:443", "router")
	if c, err := w.socksDial("job", "pypi.org:443"); err == nil {
		c.Close()
		t.Fatal("reached the router through an allowed name")
	}
	if n := w.internet.dials("192.168.1.1:443"); n != 0 {
		t.Errorf("router dialled %d times", n)
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Dest == "pypi.org:443" && e.Error != "" })
}

// DNS for a routed workload answers only addresses it may reach: an
// allowlisted name pointing at the LAN or at another qube gets no answer.
func TestDNSAnswersOnlyReachableAddresses(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_M7G9, "answer-filtered", "disallowed-name-nxdomain")
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "192.168.1.1", "10.137.0.5", "151.101.0.223")
	b := dnsQuery(t, "pypi.org", dnsmessage.TypeA)
	_, addrs := dnsAnswer(t, w.eng.AnswerDNS(context.Background(), netip.MustParseAddr("10.137.0.20"), b))
	if len(addrs) != 1 || addrs[0] != netip.MustParseAddr("151.101.0.223") {
		t.Errorf("answers %v, want only the public address", addrs)
	}
	// A name the entry allows in no zone has no such name.
	if rc, _ := dnsAnswer(t, w.eng.AnswerDNS(context.Background(), netip.MustParseAddr("10.137.0.20"),
		dnsQuery(t, "example.com", dnsmessage.TypeA))); rc != dnsmessage.RCodeNameError {
		t.Errorf("example.com: %v, want no such name", rc)
	}
	// And the router address it never got cannot be dialled either.
	d := w.eng.decide(policy.Outbound{Workload: "browser", Entry: policy.EntryIntercept,
		Addr: netip.MustParseAddr("192.168.1.1"), Port: 443, ResolvedFrom: []string{"pypi.org"}})
	if d.Verdict != policy.Refuse {
		t.Errorf("router: %+v", d)
	}
}
