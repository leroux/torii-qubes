// Package egress keeps an egress on a healthy exit node: it checks each of
// its exit nodes, uses the first healthy one in order, fails over when the
// active one goes down and fails back once a preferred one has stayed
// healthy for the hold-down. It never clears the exit node: with none
// healthy the last stays set and Usable reports false, so the engine
// refuses instead of sending anything another way.
package egress

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/Gaia-PBC/torii-qubes/proxy/internal/config"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/events"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/policy"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/tailnet"
)

const (
	// MissedPings in a row make an exit node unhealthy.
	MissedPings = 3
	// PassiveWindow is how far back real connections count: an exit node
	// is unhealthy when, within it, PassiveDestinations distinct
	// destinations failed and none succeeded.
	PassiveWindow       = 30 * time.Second
	PassiveDestinations = 3
	// FailedProbes in a row make the active exit node unhealthy.
	FailedProbes = 3
	// Interval is how often each exit node is checked.
	Interval = 10 * time.Second
)

// Options are the selector's dependencies.
type Options struct {
	Now  func() time.Time
	Log  *events.Log
	Logf func(format string, args ...any)
	// Probe, when set, is the end-to-end check through the active exit
	// node (the egress's probe address); nil error is a pass.
	Probe func(ctx context.Context) error
}

// node is what is known of one exit node.
type node struct {
	seen         bool // checked at least once
	listed       bool // in the tailnet map, online, offering an exit node
	missed       int  // pings missed in a row
	probeFails   int
	badUntil     time.Time // tripped (passive or probe) until then
	healthySince time.Time // zero while unhealthy
	why          string    // why it is unhealthy
}

func (n *node) healthy(now time.Time) bool {
	return n != nil && n.listed && n.missed < MissedPings && !now.Before(n.badUntil)
}

// choose is the exit node to use: while the active one is healthy, it
// stays, unless one ahead of it in exits has been healthy for failback;
// otherwise the first healthy one. With none healthy, the active one
// stays (it is never cleared).
func choose(exits []string, st map[string]*node, active string, now time.Time, failback time.Duration) string {
	if active != "" && st[active].healthy(now) {
		for _, e := range exits {
			if e == active {
				return active
			}
			if n := st[e]; n.healthy(now) && now.Sub(n.healthySince) >= failback {
				return e
			}
		}
		return active
	}
	for _, e := range exits {
		if st[e].healthy(now) {
			return e
		}
	}
	return active
}

// Selector is one egress's exit node selection.
type Selector struct {
	name string
	cfg  *config.Egress
	id   tailnet.Identity
	opt  Options
	kick chan struct{}

	mu          sync.Mutex
	st          map[string]*node
	active      string // the exit node set on the identity
	activeSince time.Time
	switchErr   string
	fails       map[string]time.Time // destination -> last failure, through the active node
	lastOK      time.Time
}

// New makes a selector; Run (or Tick, in tests) drives it.
func New(name string, cfg *config.Egress, id tailnet.Identity, opt Options) *Selector {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Log == nil {
		opt.Log = &events.Log{}
	}
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}
	s := &Selector{name: name, cfg: cfg, id: id, opt: opt, kick: make(chan struct{}, 1),
		st: map[string]*node{}, fails: map[string]time.Time{}}
	for _, e := range cfg.Exits {
		s.st[e] = &node{}
	}
	return s
}

func (s *Selector) failback() time.Duration { return time.Duration(s.cfg.FailbackAfter) * time.Second }

// Run checks every Interval, and at once after Report trips the passive
// trigger, until ctx ends. It waits for the identity to sign in first.
func (s *Selector) Run(ctx context.Context) {
	if err := s.id.AwaitRunning(ctx); err != nil {
		return
	}
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

// Tick checks every exit node once, then uses the one choose picks.
func (s *Selector) Tick(ctx context.Context) {
	type check struct {
		peer    tailnet.Peer
		peerErr error
		pingErr error
	}
	checks := map[string]check{}
	for _, e := range s.cfg.Exits {
		var c check
		c.peer, c.peerErr = s.id.ExitPeer(ctx, e)
		if c.peerErr == nil && c.peer.Found && c.peer.Online && c.peer.OffersExit {
			c.pingErr = s.id.Ping(ctx, e)
		}
		checks[e] = c
	}
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	var probeErr error
	if s.opt.Probe != nil && active != "" {
		probeErr = s.opt.Probe(ctx)
	}

	s.mu.Lock()
	now := s.opt.Now()
	for _, e := range s.cfg.Exits {
		n, c := s.st[e], checks[e]
		was := n.seen && n.healthy(now)
		n.listed, n.why = false, ""
		switch {
		case c.peerErr != nil:
			n.why = "tailnet map: " + c.peerErr.Error()
		case !c.peer.Found:
			n.why = "not on the tailnet"
		case !c.peer.Online:
			n.why = "offline"
		case !c.peer.OffersExit:
			n.why = "does not offer an exit node"
		default:
			n.listed = true
			if c.pingErr != nil {
				n.missed++
				n.why = "ping: " + c.pingErr.Error()
			} else {
				n.missed = 0
			}
		}
		if e == active {
			s.tripLocked(n, now, probeErr)
		}
		if n.listed && n.missed > 0 && n.missed < MissedPings {
			n.why = "" // still healthy: a ping or two may go missing
		}
		if n.listed && n.missed >= MissedPings {
			n.why = "ping: missed " + strconv.Itoa(n.missed)
		}
		if now.Before(n.badUntil) && n.why == "" {
			n.why = "tripped until " + n.badUntil.UTC().Format(time.TimeOnly)
		}
		is := n.healthy(now)
		switch {
		case is && (!n.seen || !was):
			n.healthySince = now
			if n.seen {
				s.emit(e, policy.ReasonExitHealthy, "")
			}
		case !is && n.seen && was:
			n.healthySince = time.Time{}
			s.emit(e, policy.ReasonExitUnhealthy, n.why)
		case !is:
			n.healthySince = time.Time{}
		}
		n.seen = true
	}
	target := choose(s.cfg.Exits, s.st, s.active, now, s.failback())
	s.mu.Unlock()

	if target == "" || target == active {
		return
	}
	err := s.id.SetExitNode(ctx, target)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.switchErr = err.Error()
		s.opt.Logf("egress %s: selecting %s: %v", s.name, target, err)
		return
	}
	s.switchErr = ""
	s.active, s.activeSince = target, s.opt.Now()
	s.fails, s.lastOK = map[string]time.Time{}, time.Time{}
	s.emit(target, policy.ReasonExitSwitch, "")
	s.opt.Logf("egress %s: exit node %s", s.name, target)
}

// tripLocked marks the active node down for the hold-down when the probe
// failed FailedProbes times in a row, or the passive trigger fired.
func (s *Selector) tripLocked(n *node, now time.Time, probeErr error) {
	if s.opt.Probe != nil {
		if probeErr != nil {
			n.probeFails++
		} else {
			n.probeFails = 0
		}
		if n.probeFails >= FailedProbes {
			n.badUntil, n.probeFails, n.why = now.Add(s.failback()), 0, "probe: "+probeErr.Error()
		}
	}
	for d, at := range s.fails {
		if now.Sub(at) > PassiveWindow {
			delete(s.fails, d)
		}
	}
	okRecently := !s.lastOK.IsZero() && now.Sub(s.lastOK) <= PassiveWindow
	if len(s.fails) >= PassiveDestinations && !okRecently {
		n.badUntil, n.why = now.Add(s.failback()), "connections: "+strconv.Itoa(len(s.fails))+" destinations failed, none succeeded"
		s.fails = map[string]time.Time{}
	}
}

// Report feeds a real connection's dial through the active exit node:
// nil err for a success. Enough distinct failures with no success check
// at once.
func (s *Selector) Report(dest string, err error) {
	s.mu.Lock()
	now := s.opt.Now()
	if err == nil {
		s.lastOK = now
	} else {
		s.fails[dest] = now
	}
	trip := len(s.fails) >= PassiveDestinations
	s.mu.Unlock()
	if trip {
		select {
		case s.kick <- struct{}{}:
		default:
		}
	}
}

// Usable reports whether the egress can carry a connection now: an exit
// node is set and healthy.
func (s *Selector) Usable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active != "" && s.st[s.active].healthy(s.opt.Now())
}

// Active is the exit node set on the identity, "" before the first.
func (s *Selector) Active() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Status is an egress's part of the proxy's status.
type Status struct {
	Name        string       `json:"name"`
	Active      string       `json:"active,omitempty"`
	ActiveSince time.Time    `json:"active_since,omitzero"`
	Usable      bool         `json:"usable"`
	Exits       []ExitStatus `json:"exits"`
	Error       string       `json:"error,omitempty"`
}

// ExitStatus is one exit node's health.
type ExitStatus struct {
	Name         string    `json:"name"`
	Healthy      bool      `json:"healthy"`
	HealthySince time.Time `json:"healthy_since,omitzero"`
	Why          string    `json:"why,omitempty"`
}

func (s *Selector) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opt.Now()
	st := Status{Name: s.name, Active: s.active, ActiveSince: s.activeSince, Error: s.switchErr,
		Usable: s.active != "" && s.st[s.active].healthy(now)}
	for _, e := range s.cfg.Exits {
		n := s.st[e]
		es := ExitStatus{Name: e, Healthy: n.healthy(now), HealthySince: n.healthySince, Why: n.why}
		if !n.seen {
			es.Why = "not checked yet"
		}
		st.Exits = append(st.Exits, es)
	}
	return st
}

func (s *Selector) emit(exit string, r policy.Reason, why string) {
	s.opt.Log.Emit(events.Event{Time: s.opt.Now().UTC(), Workload: config.EgressIdentityKey(s.name), Direction: events.Egress,
		Dest: exit, Reason: r, Identity: s.cfg.Hostname, Error: why})
}
