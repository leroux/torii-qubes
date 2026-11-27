package egress

import (
	"context"
	"errors"
	"github.com/leroux/torii-qubes/claims"
	"testing"
	"time"

	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

func TestChoose(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_BNFC, "first-healthy-in-order", "failback-after-hold-down")
	t0 := time.Unix(1000, 0)
	fb := 2 * time.Minute
	h := func(since time.Duration) *node { return &node{listed: true, healthySince: t0.Add(-since)} }
	down := func() *node { return &node{listed: false} }
	exits := []string{"a", "b", "c"}
	for _, tc := range []struct {
		name   string
		st     map[string]*node
		active string
		want   string
	}{
		{"first healthy from nothing", map[string]*node{"a": down(), "b": h(0), "c": h(0)}, "", "b"},
		{"none healthy keeps the last", map[string]*node{"a": down(), "b": down(), "c": down()}, "b", "b"},
		{"none healthy, never set", map[string]*node{"a": down(), "b": down(), "c": down()}, "", ""},
		{"active fails over in order", map[string]*node{"a": down(), "b": h(time.Hour), "c": h(time.Hour)}, "a", "b"},
		{"failover ignores the hold-down", map[string]*node{"a": down(), "b": h(0), "c": h(time.Hour)}, "a", "b"},
		{"failback waits", map[string]*node{"a": h(fb - time.Second), "b": h(time.Hour), "c": h(0)}, "b", "b"},
		{"failback after the hold-down", map[string]*node{"a": h(fb), "b": h(time.Hour), "c": h(0)}, "b", "a"},
		{"never moves to a later node while healthy", map[string]*node{"a": down(), "b": h(time.Hour), "c": h(time.Hour)}, "b", "b"},
		{"a missed-ping node is down", map[string]*node{"a": {listed: true, missed: 3, healthySince: t0}, "b": h(time.Hour)}, "a", "b"},
		{"a tripped node is down", map[string]*node{"a": {listed: true, badUntil: t0.Add(time.Second), healthySince: t0}, "b": h(time.Hour)}, "a", "b"},
	} {
		if got := choose(exits, tc.st, tc.active, t0, fb); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

type rig struct {
	net   *tailnet.FakeNet
	id    *tailnet.Fake
	exits map[string]*tailnet.Fake
	clk   *clock
	log   *events.Log
	s     *Selector
}

func newRig(t *testing.T, names ...string) *rig {
	t.Helper()
	r := &rig{net: &tailnet.FakeNet{}, exits: map[string]*tailnet.Fake{}, clk: &clock{time.Unix(1000, 0)}, log: &events.Log{}}
	r.id = r.net.Node("m1-egress-vpn")
	for _, n := range names {
		r.exits[n] = r.net.Node(n)
		r.exits[n].OfferExitNode()
	}
	r.s = New("vpn", &config.Egress{Hostname: "m1-egress-vpn", Exits: names, FailbackAfter: 120}, r.id, Options{
		Now: r.clk.now, Log: r.log, CheckTimeout: 20 * time.Millisecond,
	})
	return r
}

func (r *rig) tick(t *testing.T) {
	t.Helper()
	r.s.Tick(context.Background())
}

func (r *rig) set() string { return r.id.Status(context.Background()).ExitNode }

func TestSelectorFailoverAndFailback(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_8DFY, "missed-pings-down")
	r := newRig(t, "exit-1", "exit-2")
	if r.s.Usable() {
		t.Fatal("usable before any check")
	}
	r.tick(t)
	if !r.s.Usable() || r.set() != "exit-1" || r.s.Active() != "exit-1" {
		t.Fatalf("first: usable %v set %q", r.s.Usable(), r.set())
	}
	// exit-1 goes silent: three missed pings, then exit-2.
	r.exits["exit-1"].SetSilent(true)
	for i := 0; i < 2; i++ {
		r.clk.add(10 * time.Second)
		r.tick(t)
		if r.set() != "exit-1" {
			t.Fatalf("switched after %d missed pings", i+1)
		}
	}
	r.clk.add(10 * time.Second)
	r.tick(t)
	if r.set() != "exit-2" || !r.s.Usable() {
		t.Fatalf("after 3 missed: %q usable %v", r.set(), r.s.Usable())
	}
	// exit-1 back: not used until healthy for failback-after.
	r.exits["exit-1"].SetSilent(false)
	r.clk.add(10 * time.Second)
	r.tick(t)
	r.clk.add(119 * time.Second)
	r.tick(t)
	if r.set() != "exit-2" {
		t.Fatalf("failed back early")
	}
	r.clk.add(time.Second)
	r.tick(t)
	if r.set() != "exit-1" {
		t.Fatalf("no failback after the hold-down: %q", r.set())
	}
	var kinds []string
	for _, e := range r.log.Recent("egress:vpn") {
		kinds = append(kinds, string(e.Reason)+" "+e.Dest)
	}
	want := []string{"exit-switch exit-1", "exit-unhealthy exit-1", "exit-switch exit-2", "exit-healthy exit-1", "exit-switch exit-1"}
	if len(kinds) != len(want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events %v, want %v", kinds, want)
		}
	}
}

func TestSelectorNoHealthyExitKeepsTheLastAndIsNotUsable(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_6YGC, "last-kept-when-none-healthy")
	r := newRig(t, "exit-1", "exit-2")
	r.tick(t)
	r.exits["exit-1"].SetOffline(true)
	r.exits["exit-2"].SetOffline(true)
	r.clk.add(10 * time.Second)
	r.tick(t)
	if r.s.Usable() {
		t.Fatal("usable with every exit node down")
	}
	if r.set() != "exit-1" {
		t.Fatalf("the exit node was changed or cleared: %q", r.set())
	}
	// The first to come back is used at once: no hold-down with nothing up.
	r.exits["exit-2"].SetOffline(false)
	r.clk.add(10 * time.Second)
	r.tick(t)
	if !r.s.Usable() || r.set() != "exit-2" {
		t.Fatalf("recovery: usable %v set %q", r.s.Usable(), r.set())
	}
}

func TestSelectorWithdrawnExitRouteIsDown(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_8DFY, "withdrawn-route-down")
	r := newRig(t, "exit-1", "exit-2")
	r.tick(t)
	r.exits["exit-1"].StopOfferingExitNode() // online and answering, but no longer an exit node
	r.clk.add(10 * time.Second)
	r.tick(t)
	if r.set() != "exit-2" {
		t.Fatalf("a withdrawn exit node stayed active: %q", r.set())
	}
}

func TestPassiveTrigger(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_8DFY, "passive-trip")
	r := newRig(t, "exit-1", "exit-2")
	r.tick(t)
	fail := errors.New("dial: no route out")
	// Two distinct destinations failing is not enough, nor one failing three times.
	r.s.Report("a.example:443", fail)
	r.s.Report("a.example:443", fail)
	r.s.Report("b.example:443", fail)
	r.s.Report("b.example:443", fail)
	r.tick(t)
	if r.set() != "exit-1" {
		t.Fatal("tripped on two destinations")
	}
	// A success in the window clears it.
	r.s.Report("c.example:443", nil)
	r.s.Report("d.example:443", fail)
	r.tick(t)
	if r.set() != "exit-1" {
		t.Fatal("tripped despite a success")
	}
	// Three distinct, no success in 30 s: exit-1 is down for failback-after.
	r.clk.add(31 * time.Second)
	for _, d := range []string{"a.example:443", "b.example:443", "c.example:443"} {
		r.s.Report(d, fail)
	}
	r.tick(t)
	if r.set() != "exit-2" {
		t.Fatalf("passive trigger: %q", r.set())
	}
	// Its pings still answer; it stays down for the hold-down, then fails back.
	r.clk.add(119 * time.Second)
	r.tick(t)
	if r.set() != "exit-2" {
		t.Fatal("failed back while tripped")
	}
	r.clk.add(121 * time.Second)
	r.tick(t)
	r.clk.add(120 * time.Second)
	r.tick(t)
	if r.set() != "exit-1" {
		t.Fatalf("no failback after the trip: %q", r.set())
	}
}

func TestStatus(t *testing.T) {
	r := newRig(t, "exit-1", "exit-2")
	r.exits["exit-1"].SetOffline(true)
	r.tick(t)
	st := r.s.Status()
	if st.Name != "vpn" || st.Active != "exit-2" || !st.Usable || len(st.Exits) != 2 || !st.ActiveSince.Equal(r.clk.now()) {
		t.Fatalf("%+v", st)
	}
	if st.Exits[0].Healthy || st.Exits[0].Why == "" || !st.Exits[1].Healthy {
		t.Fatalf("%+v", st.Exits)
	}
}

// A named probe trips the active node after three failures in a row; an
// egress that names none runs no probe.
func TestProbeTripsTheActiveNode(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_8DFY, "probe-trips")
	r := newRig(t, "exit-1", "exit-2")
	calls, fail := 0, true
	r.s = New("vpn", &config.Egress{Hostname: "m1-egress-vpn", Exits: []string{"exit-1", "exit-2"}, FailbackAfter: 120}, r.id, Options{
		Now: r.clk.now, Log: r.log, CheckTimeout: 20 * time.Millisecond,
		Probe: func(ctx context.Context) error {
			calls++
			if fail {
				return errors.New("probe: no answer")
			}
			return nil
		},
	})
	r.tick(t) // selects exit-1; no probe before an exit node is active
	if calls != 0 || r.set() != "exit-1" {
		t.Fatalf("calls %d, set %q", calls, r.set())
	}
	for i := 1; i <= 2; i++ {
		r.clk.add(10 * time.Second)
		r.tick(t)
		if r.set() != "exit-1" {
			t.Fatalf("tripped after %d failed probes", i)
		}
	}
	r.clk.add(10 * time.Second)
	r.tick(t)
	if r.set() != "exit-2" || calls != 3 {
		t.Fatalf("after 3 failed probes: set %q, %d probes", r.set(), calls)
	}
	// With no probe named, the selector has none to run.
	plain := newRig(t, "exit-1")
	if plain.s.opt.Probe != nil {
		t.Error("a probe with none named")
	}
}
