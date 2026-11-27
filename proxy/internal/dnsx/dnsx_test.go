package dnsx

import (
	"github.com/leroux/torii-qubes/claims"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func query(t *testing.T, name string, typ dnsmessage.Type) []byte {
	t.Helper()
	m := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 42, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}},
	}
	b, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseAnswer(t *testing.T) {
	q, err := Parse(query(t, "PyPI.org.", dnsmessage.TypeA))
	if err != nil || q.Name != "pypi.org" || q.Type != dnsmessage.TypeA {
		t.Fatalf("%+v %v", q, err)
	}
	b, err := Answer(q, []netip.Addr{netip.MustParseAddr("1.2.3.4"), netip.MustParseAddr("::1")}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		t.Fatal(err)
	}
	if m.ID != 42 || !m.Response || len(m.Answers) != 1 {
		t.Fatalf("%+v", m)
	}
	if a := m.Answers[0].Body.(*dnsmessage.AResource).A; a != [4]byte{1, 2, 3, 4} {
		t.Errorf("answer %v", a)
	}
	b, _ = Error(q, dnsmessage.RCodeNameError)
	m.Unpack(b)
	if m.RCode != dnsmessage.RCodeNameError || len(m.Answers) != 0 {
		t.Errorf("%+v", m.Header)
	}
	if _, err := Parse([]byte{1, 2, 3}); err == nil {
		t.Error("malformed accepted")
	}
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func TestCacheExpires(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_2R9T, "other-qubes-resolution-not-shared", "mapping-expires")
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := &Cache{Clock: clk, MinTTL: 30 * time.Second}
	a := netip.MustParseAddr("1.2.3.4")
	c.Record("w", "pypi.org", []netip.Addr{a}, time.Second) // raised to MinTTL
	c.Record("w", "cdn.net", []netip.Addr{a}, 5*time.Minute)
	if got := c.Names("w", a); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := c.Names("other", a); len(got) != 0 {
		t.Errorf("names leak between workloads: %v", got)
	}
	clk.t = clk.t.Add(time.Minute)
	if got := c.Names("w", a); len(got) != 1 || got[0] != "cdn.net" {
		t.Errorf("after a minute: %v", got)
	}
	clk.t = clk.t.Add(time.Hour)
	if got := c.Names("w", a); len(got) != 0 {
		t.Errorf("after an hour: %v", got)
	}
}
