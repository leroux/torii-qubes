// Package engine is the proxy program: it decides each connection with
// package policy and carries the allowed ones. Every outside dependency
// comes in through Deps, so tests run it with fakes, testcontrol, network
// namespaces or Headscale.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Gaia-PBC/torii-qubes/proxy/internal/config"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/dnsx"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/egress"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/events"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/policy"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/sni"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/socks5"
	"github.com/Gaia-PBC/torii-qubes/proxy/internal/tailnet"
)

// Dialer opens TCP connections; *net.Dialer is one.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// Resolver looks names up; *net.Resolver is one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// QubeDialer connects into a qube with no network card, over qrexec
// (qubes.ConnectTCP in the workload, allowed by dom0 policy).
type QubeDialer interface {
	DialQube(ctx context.Context, qube string, port uint16) (net.Conn, error)
}

// Deps is everything the engine talks to.
type Deps struct {
	Config *config.Config
	// Identities by workload name; an egress's under
	// config.EgressIdentityKey(name).
	Identities map[string]tailnet.Identity
	// Internet dials through the proxy qube's own netvm.
	Internet Dialer
	// Resolver resolves internet names on the direct path.
	Resolver Resolver
	// ExitResolver resolves internet names through an identity's exit
	// node; default: DNS over TCP to Config.ExitDNS through the identity.
	ExitResolver func(id tailnet.Identity) Resolver
	// Qubes connects into qrexec workloads.
	Qubes QubeDialer
	// Local dials routed workloads (and the proxy qube itself) directly.
	Local Dialer
	Log   *events.Log
	Clock dnsx.Clock
	Logf  func(format string, args ...any)
	// SNIWait bounds how long an intercepted connection's first bytes
	// are waited for before deciding without a server name.
	SNIWait     time.Duration
	DialTimeout time.Duration
	// UDPIdle ends a UDP flow after this long without traffic either way.
	UDPIdle time.Duration
	// MaxUDPFlows caps a workload's UDP flows, refused ones included, so
	// one workload cannot exhaust the proxy qube.
	MaxUDPFlows int
	// SelfAddrs lists the proxy qube's own addresses, never dialled on
	// the internet path; default: its interfaces' addresses.
	SelfAddrs func() []netip.Addr
}

// Engine is a running proxy.
type Engine struct {
	d     Deps
	cache *dnsx.Cache

	egress map[string]*egress.Selector // by egress name; set by Start

	mu       sync.Mutex
	active   map[string]int
	lnErrs   map[string]string
	lns      []net.Listener
	local    map[string]string // local listener name -> address
	flows    map[string]*udpFlow
	nflows   map[string]int       // per workload
	limitLog map[string]time.Time // last flow-limit event per workload
	self     []netip.Addr
	selfAt   time.Time
	closed   bool
	wg       sync.WaitGroup
	cancel   context.CancelFunc
}

// New makes an engine; Start brings up identities and listeners.
func New(d Deps) *Engine {
	if d.Log == nil {
		d.Log = &events.Log{}
	}
	if d.Clock == nil {
		d.Clock = dnsx.RealClock{}
	}
	if d.Logf == nil {
		d.Logf = func(string, ...any) {}
	}
	if d.SNIWait == 0 {
		d.SNIWait = 2 * time.Second
	}
	if d.DialTimeout == 0 {
		d.DialTimeout = 15 * time.Second
	}
	if d.UDPIdle == 0 {
		d.UDPIdle = 60 * time.Second
	}
	if d.MaxUDPFlows == 0 {
		d.MaxUDPFlows = 512
	}
	if d.SelfAddrs == nil {
		d.SelfAddrs = interfaceAddrs
	}
	if d.Internet == nil {
		d.Internet = &net.Dialer{}
	}
	if d.Local == nil {
		d.Local = &net.Dialer{}
	}
	if d.Resolver == nil {
		d.Resolver = net.DefaultResolver
	}
	if d.ExitResolver == nil {
		exitDNS := d.Config.ExitDNS
		d.ExitResolver = func(id tailnet.Identity) Resolver { return ExitResolver(id, exitDNS) }
	}
	return &Engine{
		d:        d,
		cache:    &dnsx.Cache{Clock: d.Clock, MinTTL: 5 * time.Minute},
		active:   map[string]int{},
		lnErrs:   map[string]string{},
		local:    map[string]string{},
		egress:   map[string]*egress.Selector{},
		flows:    map[string]*udpFlow{},
		nflows:   map[string]int{},
		limitLog: map[string]time.Time{},
	}
}

func interfaceAddrs() []netip.Addr {
	var out []netip.Addr
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr().Unmap())
		}
	}
	return out
}

// forbidden is policy.Forbidden plus, on the direct internet path, the
// proxy qube's own addresses: a workload must not reach the proxy's own
// sockets by dialling them. Through an exit node they are the exit
// node's network, not this qube.
func (e *Engine) forbidden(a netip.Addr, path policy.Path) bool {
	if policy.Forbidden(a) {
		return true
	}
	if path != policy.PathInternet {
		return false
	}
	e.mu.Lock()
	if e.self == nil || time.Since(e.selfAt) > 5*time.Second {
		e.self, e.selfAt = e.d.SelfAddrs(), time.Now()
	}
	self := e.self
	e.mu.Unlock()
	a = a.Unmap()
	for _, s := range self {
		if s == a {
			return true
		}
	}
	return false
}

// decide is policy.DecideOutbound with the proxy qube's own addresses
// forbidden off the tailnet, and the exit path refused while the
// workload's egress has no healthy exit node.
func (e *Engine) decide(o policy.Outbound) policy.Decision {
	d := policy.DecideOutbound(e.d.Config, o)
	if d.Verdict == policy.Allow && d.Path == policy.PathInternet && o.Addr.IsValid() && e.forbidden(o.Addr, d.Path) {
		return policy.Decision{Verdict: policy.Refuse, Reason: policy.ReasonForbiddenAddress}
	}
	return e.exitUsable(o.Workload, d)
}

// exitUsable refuses an exit-path decision whose egress has no healthy
// exit node: the connection fails at once instead of hanging, and is
// never sent another way.
func (e *Engine) exitUsable(workload string, d policy.Decision) policy.Decision {
	if d.Verdict != policy.Allow || d.Path != policy.PathExit {
		return d
	}
	if s := e.selectorOf(workload); s == nil || !s.Usable() {
		return policy.Decision{Verdict: policy.Refuse, Reason: policy.ReasonNoHealthyExit, Path: policy.PathExit, Identity: d.Identity}
	}
	return d
}

// selectorOf is the selector of a workload's egress, or nil.
func (e *Engine) selectorOf(workload string) *egress.Selector {
	w, ok := e.d.Config.Lookup(workload)
	if !ok || w.Egress == "" {
		return nil
	}
	return e.egress[w.Egress]
}

// exitIdentity is the identity a workload's exit path dials through: its
// egress's, never its own. nil when it has none.
func (e *Engine) exitIdentity(workload string) tailnet.Identity {
	w, ok := e.d.Config.Lookup(workload)
	if !ok || w.Egress == "" {
		return nil
	}
	return e.d.Identities[config.EgressIdentityKey(w.Egress)]
}

// dialExit dials through the workload's egress, and tells its selector
// how it went (the passive health check).
func (e *Engine) dialExit(ctx context.Context, workload string, dst netip.AddrPort, udp bool) (net.Conn, error) {
	id := e.exitIdentity(workload)
	if id == nil {
		return nil, tailnet.ErrNoRoute
	}
	if udp {
		return id.DialUDP(ctx, dst) // connectionless: says nothing of health
	}
	c, err := id.Dial(ctx, dst)
	if s := e.selectorOf(workload); s != nil && ctx.Err() == nil {
		s.Report(dst.String(), err)
	}
	return c, err
}

// ExitResolver resolves through id's exit node: DNS over TCP to server,
// dialled through the identity, so lookups never touch the local network.
func ExitResolver(id tailnet.Identity, server string) Resolver {
	ap := netip.MustParseAddrPort(server)
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			// Not a PacketConn, so the Go resolver frames DNS over TCP.
			return id.Dial(ctx, ap)
		},
	}
}

// Log is the decision log.
func (e *Engine) Log() *events.Log { return e.d.Log }

// Start starts every identity, its inbound listeners, and each egress's
// exit-node selection. It does not wait for sign-in.
func (e *Engine) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	c := e.d.Config
	for _, name := range c.EgressNames() {
		g := c.Egresses[name]
		id := e.d.Identities[config.EgressIdentityKey(name)]
		if id == nil {
			return fmt.Errorf("engine: no identity for egress %s", name)
		}
		if err := id.Start(ctx); err != nil {
			return fmt.Errorf("engine: start egress %s: %w", name, err)
		}
		opt := egress.Options{Log: e.d.Log, Logf: e.d.Logf, Now: e.d.Clock.Now}
		if g.Probe != "" {
			probe := g.Probe
			opt.Probe = func(ctx context.Context) error { return e.probe(ctx, id, probe) }
		}
		s := egress.New(name, g, id, opt)
		e.egress[name] = s
		e.wg.Add(1)
		go func() { defer e.wg.Done(); s.Run(ctx) }()
	}
	for _, name := range c.WorkloadNames() {
		w := c.Workloads[name]
		id := e.d.Identities[name]
		if w.Identity == nil {
			continue
		}
		if id == nil {
			return fmt.Errorf("engine: no identity for %s", name)
		}
		if err := id.Start(ctx); err != nil {
			return fmt.Errorf("engine: start %s: %w", name, err)
		}
		for _, in := range w.Inbound {
			listen := id.Listen
			if in.TLS {
				listen = id.ListenTLS
			}
			ln, err := listen(in.Port)
			if err != nil {
				e.mu.Lock()
				e.lnErrs[fmt.Sprintf("%s:%d", name, in.Port)] = err.Error()
				e.mu.Unlock()
				continue
			}
			e.track(ln)
			e.wg.Add(1)
			go e.serveInbound(ctx, name, in.Port, ln, id)
		}
	}
	if err := e.startLocal(ctx); err != nil {
		return err
	}
	e.wg.Add(1)
	go e.sweepUDP(ctx)
	return nil
}

// startLocal serves the proxy qube's own programs (the link: none
// workload): its SOCKS port and its forwards, on loopback.
func (e *Engine) startLocal(ctx context.Context) error {
	c := e.d.Config
	w, ok := c.Workloads[c.ProxyQube]
	if !ok || w.Link != config.LinkNone {
		return nil
	}
	serve := func(name, addr string, h func(net.Conn)) error {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("engine: %s on %s: %w", name, addr, err)
		}
		e.track(ln)
		e.mu.Lock()
		e.local[name] = ln.Addr().String()
		e.mu.Unlock()
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { defer conn.Close(); h(conn) }()
			}
		}()
		return nil
	}
	if w.SOCKS {
		if err := serve("socks", c.Listen.LocalSOCKS, func(conn net.Conn) { e.handleSOCKS(ctx, c.ProxyQube, conn) }); err != nil {
			return err
		}
	}
	for _, f := range w.Forwards {
		f := f
		addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(f.Listen))
		if err := serve("f."+f.Name, addr, func(conn net.Conn) { e.handleForward(ctx, c.ProxyQube, f.Name, conn) }); err != nil {
			return err
		}
	}
	return nil
}

// LocalAddrs names the loopback listeners of the proxy qube's own
// programs ("socks", "f.<name>") and their addresses.
func (e *Engine) LocalAddrs() map[string]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]string{}
	for k, v := range e.local {
		out[k] = v
	}
	return out
}

func (e *Engine) track(ln net.Listener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lns = append(e.lns, ln)
}

// probe dials an egress's probe address through its identity, resolving
// a name through the exit node, and closes it: nil when it connected.
func (e *Engine) probe(ctx context.Context, id tailnet.Identity, hostport string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	host, port, err := config.SplitHostPort(hostport)
	if err != nil {
		return err
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		addrs, lerr := e.d.ExitResolver(id).LookupNetIP(ctx, "ip4", host)
		if lerr != nil || len(addrs) == 0 {
			return fmt.Errorf("probe %s: %v", host, lerr)
		}
		a = addrs[0]
	}
	c, err := id.Dial(ctx, netip.AddrPortFrom(a, port))
	if err != nil {
		return err
	}
	return c.Close()
}

// Close stops listeners and identities.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	lns := e.lns
	e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	for _, ln := range lns {
		ln.Close()
	}
	e.mu.Lock()
	flows := e.flows
	e.flows = map[string]*udpFlow{}
	e.mu.Unlock()
	for _, f := range flows {
		f.close()
	}
	for _, id := range e.d.Identities {
		id.Close()
	}
	e.wg.Wait()
	return nil
}

func (e *Engine) inc(w string, d int) {
	e.mu.Lock()
	e.active[w] += d
	e.mu.Unlock()
}

// ---- inbound ----

func (e *Engine) serveInbound(ctx context.Context, workload string, port uint16, ln net.Listener, id tailnet.Identity) {
	defer e.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go e.handleInbound(ctx, workload, port, c, id)
	}
}

func (e *Engine) handleInbound(ctx context.Context, workload string, port uint16, c net.Conn, id tailnet.Identity) {
	defer c.Close()
	ev := events.Event{Workload: workload, Direction: events.In, Dest: fmt.Sprintf("%s:%d", workload, port)}
	ev.Source = id.WhoIs(ctx, c.RemoteAddr().String())
	d, target := policy.DecideInbound(e.d.Config, workload, port)
	fill(&ev, d)
	if d.Verdict != policy.Allow {
		e.d.Log.Emit(ev)
		return
	}
	dctx, cancel := context.WithTimeout(ctx, e.d.DialTimeout)
	up, err := e.dialWorkload(dctx, workload, target)
	cancel()
	if err != nil {
		ev.Error = err.Error()
		e.d.Log.Emit(ev)
		return
	}
	defer up.Close()
	e.d.Log.Emit(ev)
	e.inc(workload, 1)
	defer e.inc(workload, -1)
	relay(c, nil, up)
}

func (e *Engine) dialWorkload(ctx context.Context, workload string, port uint16) (net.Conn, error) {
	w, _ := e.d.Config.Lookup(workload)
	switch w.Link {
	case config.LinkQrexec, config.LinkRouted:
		// Routed workloads too: a Qubes app qube's own firewall drops new
		// connections from its netvm, and opening it would mean editing
		// the workload. qubes.ConnectTCP needs nothing in the workload,
		// and dom0 policy allows it per declared port.
		if e.d.Qubes == nil {
			return nil, errors.New("engine: no qrexec dialer")
		}
		qube := workload
		if w.Qube != "" {
			qube = w.Qube
		}
		return e.d.Qubes.DialQube(ctx, qube, port)
	default:
		return e.d.Local.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	}
}

// ---- outbound, qrexec workloads ----

// HandleQrexec serves one torii.Connect call: caller is the calling qube
// as dom0 named it, arg the service argument ("socks" or "f.<name>").
func (e *Engine) HandleQrexec(ctx context.Context, caller, arg string, c net.Conn) {
	defer c.Close()
	if w, ok := e.d.Config.Lookup(caller); !ok || w.Link != config.LinkQrexec {
		reason := policy.ReasonWrongEntry
		if !ok {
			reason = policy.ReasonUnknownWorkload
		}
		e.d.Log.Emit(events.Event{Workload: caller, Direction: events.Out, Source: caller, Dest: arg,
			Verdict: policy.Refuse, Reason: reason})
		return
	}
	switch {
	case arg == "socks":
		e.handleSOCKS(ctx, caller, c)
	case arg == "http":
		e.handleHTTP(ctx, caller, c)
	case strings.HasPrefix(arg, "f."):
		e.handleForward(ctx, caller, strings.TrimPrefix(arg, "f."), c)
	default:
		e.d.Log.Emit(events.Event{Workload: caller, Direction: events.Out, Source: caller, Dest: arg,
			Verdict: policy.Refuse, Reason: policy.ReasonWrongEntry})
	}
}

func (e *Engine) handleForward(ctx context.Context, workload, name string, c net.Conn) {
	to, ok := policy.ForwardTarget(e.d.Config, workload, name)
	if !ok {
		e.d.Log.Emit(events.Event{Workload: workload, Direction: events.Out, Entry: policy.EntryForward, Source: workload,
			Dest: "f." + name, Verdict: policy.Refuse, Reason: policy.ReasonUnknownForward})
		return
	}
	host, port, _ := config.SplitHostPort(to)
	o := e.classify(ctx, policy.Outbound{Workload: workload, Entry: policy.EntryForward, Port: port}, host)
	up, d, ev := e.decideAndDial(ctx, &o, to)
	if up == nil {
		return
	}
	_ = d
	defer up.Close()
	e.carry(ctx, o, ev, c, up, false)
}

func (e *Engine) handleSOCKS(ctx context.Context, workload string, c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	req, err := socks5.Handshake(c)
	_ = c.SetDeadline(time.Time{})
	if err != nil {
		return
	}
	host := req.Host
	if host == "" {
		host = req.Addr.String()
	}
	o := e.classify(ctx, policy.Outbound{Workload: workload, Entry: policy.EntrySOCKS, Port: req.Port}, host)
	up, d, ev := e.decideAndDial(ctx, &o, req.Target())
	if up == nil {
		code := socks5.NotAllowed
		if d.Verdict == policy.Allow {
			code = socks5.HostUnreachable
		}
		socks5.Reply(c, code)
		return
	}
	defer up.Close()
	if err := socks5.Reply(c, socks5.Succeeded); err != nil {
		return
	}
	e.carry(ctx, o, ev, c, up, true)
}

// classify fills Host/Addr and whether the destination is on the tailnet.
func (e *Engine) classify(ctx context.Context, o policy.Outbound, host string) policy.Outbound {
	id := e.d.Identities[o.Workload]
	if a, err := netip.ParseAddr(host); err == nil {
		o.Addr = a.Unmap()
		if id != nil && e.hasIdentity(o.Workload) {
			o.Tailnet = id.IsTailnet(ctx, o.Addr)
			if o.Tailnet {
				o.PeerNames = id.PeerNames(ctx, o.Addr)
			}
		}
		return o
	}
	o.Host = strings.TrimSuffix(strings.ToLower(host), ".")
	if id != nil && e.hasIdentity(o.Workload) {
		if _, ok := id.LookupTailnet(ctx, o.Host); ok {
			o.Tailnet = true
		}
	}
	return o
}

func (e *Engine) hasIdentity(w string) bool {
	wl, _ := e.d.Config.Lookup(w)
	return wl != nil && wl.Identity != nil
}

// decideAndDial decides o and, if allowed, dials. It returns a nil conn
// when refused or when the dial failed; the event is emitted then. When
// it returns a conn, the event is returned for carry to emit.
// When o is a name, decideAndDial sets o.Addr to the address it dialled,
// so later checks (the TLS server name) decide on what was really reached.
func (e *Engine) decideAndDial(ctx context.Context, o *policy.Outbound, dest string) (net.Conn, policy.Decision, events.Event) {
	d := e.decide(*o)
	ev := events.Event{Workload: o.Workload, Direction: events.Out, Entry: o.Entry, Source: o.Workload, Dest: dest, SNI: o.SNI}
	fill(&ev, d)
	if d.Verdict != policy.Allow {
		e.d.Log.Emit(ev)
		return nil, d, ev
	}
	dctx, cancel := context.WithTimeout(ctx, e.d.DialTimeout)
	defer cancel()
	up, err := e.dial(dctx, o, d)
	if err != nil {
		ev.Error = err.Error()
		e.d.Log.Emit(ev)
		return nil, d, ev
	}
	return up, d, ev
}

// dial carries an allowed decision.
func (e *Engine) dial(ctx context.Context, o *policy.Outbound, d policy.Decision) (net.Conn, error) {
	id := e.d.Identities[o.Workload]
	var addrs []netip.Addr
	switch {
	case o.Addr.IsValid():
		addrs = []netip.Addr{o.Addr}
	case d.Path == policy.PathTailnet:
		a, _ := id.LookupTailnet(ctx, o.Host)
		if len(a) == 0 {
			return nil, fmt.Errorf("no tailnet node %s", o.Host)
		}
		addrs = a
	default:
		r := e.d.Resolver
		if d.Path == policy.PathExit {
			xid := e.exitIdentity(o.Workload)
			if xid == nil {
				return nil, tailnet.ErrNoRoute
			}
			r = e.d.ExitResolver(xid)
		}
		a, err := r.LookupNetIP(ctx, "ip", o.Host)
		if err != nil {
			return nil, err
		}
		w, _ := e.d.Config.Lookup(o.Workload)
		for _, x := range a {
			// Each address is decided by its zone: a name allowed on the
			// internet that resolves to the LAN, to another qube or to the
			// proxy qube itself is not dialled there.
			r, _ := policy.Permit(w, []string{o.Host}, x, o.Port, "tcp", "")
			if allowed(r) && !e.forbidden(x, policy.PathFor(w, x)) {
				addrs = append(addrs, x.Unmap())
			}
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("%s resolves to no address this workload may reach", o.Host)
		}
	}
	sort.SliceStable(addrs, func(i, j int) bool { return addrs[i].Is4() && !addrs[j].Is4() })
	var lastErr error
	for _, a := range addrs {
		ap := netip.AddrPortFrom(a, o.Port)
		var c net.Conn
		var err error
		path := d.Path
		if path != policy.PathTailnet && !o.Addr.IsValid() {
			w, _ := e.d.Config.Lookup(o.Workload)
			path = policy.PathFor(w, a)
		}
		switch path {
		case policy.PathTailnet:
			c, err = id.Dial(ctx, ap)
		case policy.PathExit:
			c, err = e.dialExit(ctx, o.Workload, ap, false)
		default:
			c, err = e.d.Internet.DialContext(ctx, "tcp", ap.String())
		}
		if err == nil {
			if !o.Addr.IsValid() {
				o.Addr = a
				if path == policy.PathTailnet {
					o.PeerNames = id.PeerNames(ctx, a)
				}
			}
			return c, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// carry emits the event and relays. Where TLS server names count (an
// allowlist), the client's bytes are held until its first ones have been
// read: a ClientHello's name is decided before anything of the client's
// goes upstream, however long the client waits to send it. The server's
// side flows from the start, so a server that speaks first can.
func (e *Engine) carry(ctx context.Context, o policy.Outbound, ev events.Event, c, up net.Conn, checkSNI bool) {
	w, _ := e.d.Config.Lookup(o.Workload)
	check := func() ([]byte, bool) {
		e.d.Log.Emit(ev)
		return nil, true
	}
	if checkSNI && ((usesAllowlist(w) && !o.Tailnet) || (o.Tailnet && len(w.TailnetAllow) > 0)) {
		// o.Addr is the address dialled (decideAndDial set it for a name),
		// so the server name is decided against the zone really reached.
		check = func() ([]byte, bool) {
			name, p, ok := e.peekName(c, ev)
			if !ok {
				return nil, false
			}
			if name != "" {
				o.SNI = name
				ev.SNI = name
				if d := e.decide(o); d.Verdict != policy.Allow {
					fill(&ev, d)
					e.d.Log.Emit(ev)
					return nil, false
				}
			}
			e.d.Log.Emit(ev)
			return p, true
		}
	}
	e.inc(o.Workload, 1)
	defer e.inc(o.Workload, -1)
	relayChecked(c, up, check)
}

// ---- outbound, routed workloads ----

// HandleIntercepted serves a redirected connection from a routed
// workload: src names the workload, dst is where it was going.
func (e *Engine) HandleIntercepted(ctx context.Context, c net.Conn, src, dst netip.AddrPort) {
	defer c.Close()
	name, _, ok := e.d.Config.ByAddress(src.Addr())
	if !ok {
		e.d.Log.Emit(events.Event{Workload: "-", Direction: events.Out, Entry: policy.EntryIntercept, Source: src.Addr().String(),
			Dest: dst.String(), Verdict: policy.Refuse, Reason: policy.ReasonUnknownWorkload})
		return
	}
	o := e.classify(ctx, policy.Outbound{Workload: name, Entry: policy.EntryIntercept, Port: dst.Port()}, dst.Addr().String())
	if !o.Tailnet {
		o.ResolvedFrom = e.cache.Names(name, o.Addr)
	}
	// Decided on the address first: a server name only ever refuses
	// more, and carry reads it, if it counts, before relaying the client.
	up, _, ev := e.decideAndDial(ctx, &o, dst.String())
	if up == nil {
		return
	}
	defer up.Close()
	e.carry(ctx, o, ev, c, up, true)
}

// ---- DNS, routed workloads ----

// AnswerDNS answers one query from a routed workload at src. A refused
// name gets NXDOMAIN; an unknown source gets REFUSED.
func (e *Engine) AnswerDNS(ctx context.Context, src netip.Addr, b []byte) []byte {
	q, err := dnsx.Parse(b)
	if err != nil {
		if len(q.Msg.Questions) > 0 {
			out, _ := dnsx.Error(q, 1) // FORMERR
			return out
		}
		return nil
	}
	name, w, ok := e.d.Config.ByAddress(src)
	ev := events.Event{Workload: name, Direction: events.DNS, Source: src.String(), Dest: q.Name + " " + q.Type.String()}
	if !ok {
		ev.Workload, ev.Verdict, ev.Reason = "-", policy.Refuse, policy.ReasonUnknownWorkload
		e.d.Log.Emit(ev)
		out, _ := dnsx.Error(q, 5) // REFUSED
		return out
	}
	id := e.d.Identities[name]
	var tailAddrs []netip.Addr
	isTail := false
	if w.Identity != nil && id != nil {
		tailAddrs, isTail = id.LookupTailnet(ctx, q.Name)
	}
	d := e.exitUsable(name, policy.DecideName(e.d.Config, name, q.Name, isTail))
	fill(&ev, d)
	if d.Reason == policy.ReasonNoHealthyExit {
		e.d.Log.Emit(ev)
		out, _ := dnsx.Error(q, 2) // SERVFAIL: no way out, not "no such name"
		return out
	}
	if d.Verdict != policy.Allow {
		e.d.Log.Emit(ev)
		out, _ := dnsx.Error(q, 3) // NXDOMAIN
		return out
	}
	var addrs []netip.Addr
	if isTail {
		if len(tailAddrs) == 0 {
			e.d.Log.Emit(ev)
			out, _ := dnsx.Error(q, 3)
			return out
		}
		addrs = tailAddrs
	} else if q.Type == 1 || q.Type == 28 { // A, AAAA
		r := e.d.Resolver
		if d.Path == policy.PathExit {
			r = e.d.ExitResolver(e.exitIdentity(name))
		}
		network := "ip4"
		if q.Type == 28 {
			network = "ip6"
		}
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		got, err := r.LookupNetIP(lctx, network, q.Name)
		cancel()
		var dnsErr *net.DNSError
		if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
			ev.Error = err.Error()
			e.d.Log.Emit(ev)
			out, _ := dnsx.Error(q, 2) // SERVFAIL
			return out
		}
		for _, a := range got {
			// Answer only addresses this workload may reach under the name,
			// so a public name cannot point it at the LAN or another qube.
			if r, _ := policy.Permit(w, []string{q.Name}, a, 0, "", ""); allowed(r) && !e.forbidden(a, policy.PathFor(w, a)) {
				addrs = append(addrs, a.Unmap())
			}
		}
		e.cache.Record(name, q.Name, addrs, time.Minute)
	}
	e.d.Log.Emit(ev)
	out, _ := dnsx.Answer(q, addrs, time.Minute)
	return out
}

// ---- explain and status ----

// ExplainRequest asks whether a connection would be allowed.
type ExplainRequest struct {
	Workload  string `json:"workload"`
	Direction string `json:"direction,omitempty"` // "out" (default) or "in"
	Dest      string `json:"dest"`                // host:port; for "in", the port
	SNI       string `json:"sni,omitempty"`
}

// Explanation is the answer, with what the decision rested on.
type Explanation struct {
	policy.Decision
	Tailnet      bool     `json:"tailnet"`
	ResolvedFrom []string `json:"resolved_from,omitempty"`
}

// Explain decides without sending traffic.
func (e *Engine) Explain(ctx context.Context, r ExplainRequest) (Explanation, error) {
	w, ok := e.d.Config.Lookup(r.Workload)
	if !ok {
		return Explanation{Decision: policy.Decision{Verdict: policy.Refuse, Reason: policy.ReasonUnknownWorkload}}, nil
	}
	if r.Direction == "in" {
		var port uint16
		if _, err := fmt.Sscan(strings.TrimPrefix(r.Dest, ":"), &port); err != nil {
			return Explanation{}, fmt.Errorf("explain: in needs a port")
		}
		d, _ := policy.DecideInbound(e.d.Config, r.Workload, port)
		return Explanation{Decision: d}, nil
	}
	host, port, err := config.SplitHostPort(r.Dest)
	if err != nil {
		return Explanation{}, err
	}
	entry := policy.EntrySOCKS
	switch w.Link {
	case config.LinkRouted:
		entry = policy.EntryIntercept
	}
	o := e.classify(ctx, policy.Outbound{Workload: r.Workload, Entry: entry, Port: port, SNI: strings.ToLower(r.SNI)}, host)
	if entry == policy.EntryIntercept && o.Host != "" {
		// A routed workload connects to addresses; it asked DNS for the
		// name first.
		o.ResolvedFrom, o.Host = []string{o.Host}, ""
	} else if entry == policy.EntryIntercept {
		o.ResolvedFrom = e.cache.Names(r.Workload, o.Addr)
	}
	return Explanation{Decision: e.decide(o), Tailnet: o.Tailnet, ResolvedFrom: o.ResolvedFrom}, nil
}

// Status is a snapshot of the running program.
type Status struct {
	Version   string                    `json:"config_version"`
	ProxyQube string                    `json:"proxy_qube"`
	Workloads map[string]WorkloadStatus `json:"workloads"`
	Egresses  map[string]EgressStatus   `json:"egresses,omitempty"`
}

// EgressStatus is one egress: its identity and its exit nodes' health.
type EgressStatus struct {
	egress.Status
	Identity *tailnet.Status `json:"identity,omitempty"`
}

// WorkloadStatus is one workload's part of the status.
type WorkloadStatus struct {
	Link     config.Link     `json:"link"`
	Identity *tailnet.Status `json:"identity,omitempty"`
	Active   int             `json:"active_connections"`
	Errors   []string        `json:"errors,omitempty"`
}

// Status reports identities, sign-in state and active connections.
func (e *Engine) Status(ctx context.Context) Status {
	c := e.d.Config
	st := Status{Version: c.Version, ProxyQube: c.ProxyQube, Workloads: map[string]WorkloadStatus{}}
	for _, name := range c.WorkloadNames() {
		w := c.Workloads[name]
		ws := WorkloadStatus{Link: w.Link}
		if id := e.d.Identities[name]; id != nil && w.Identity != nil {
			s := id.Status(ctx)
			ws.Identity = &s
		}
		e.mu.Lock()
		ws.Active = e.active[name]
		for k, v := range e.lnErrs {
			if strings.HasPrefix(k, name+":") {
				ws.Errors = append(ws.Errors, "listen "+k+": "+v)
			}
		}
		e.mu.Unlock()
		st.Workloads[name] = ws
	}
	for name, s := range e.egress {
		if st.Egresses == nil {
			st.Egresses = map[string]EgressStatus{}
		}
		es := EgressStatus{Status: s.Status()}
		if id := e.d.Identities[config.EgressIdentityKey(name)]; id != nil {
			is := id.Status(ctx)
			es.Identity = &is
		}
		st.Egresses[name] = es
	}
	return st
}

// ---- helpers ----

// peekName reads the TLS server name of a connection whose decision needs
// it. Every way it can end the connection is logged: an unreadable
// ClientHello is refused, and a client that closes or fails before its
// first bytes is closed (no decision, nothing relayed). ev is the event
// so far; ok is false when the connection must end.
func (e *Engine) peekName(c net.Conn, ev events.Event) (name string, prefix []byte, ok bool) {
	name, prefix, err := sni.Peek(c, e.d.SNIWait)
	switch {
	case err == nil:
		return name, prefix, true
	case errors.Is(err, sni.ErrUnreadable):
		fill(&ev, policy.Decision{Verdict: policy.Refuse, Reason: policy.ReasonSNIUnreadable})
	default:
		fill(&ev, policy.Decision{Verdict: policy.Closed, Reason: policy.ReasonClosedBeforeData})
		ev.Error = err.Error()
	}
	e.d.Log.Emit(ev)
	return "", nil, false
}

func fill(ev *events.Event, d policy.Decision) {
	ev.Verdict, ev.Reason, ev.Path, ev.Identity, ev.Host = d.Verdict, d.Reason, d.Path, d.Identity, d.Host
}

type closeWriter interface{ CloseWrite() error }

// relay copies both ways until both sides are done. prefix, the bytes
// already read from c (a peeked ClientHello), goes first; the rest is
// copied from the raw connection, so two *net.TCPConn relay inside the
// kernel (splice).
func relay(c net.Conn, prefix []byte, up net.Conn) {
	relayChecked(c, up, func() ([]byte, bool) { return prefix, true })
}

// relayChecked is relay, with the client's side held until check has
// run: upstream to the client flows from the start; the client's bytes
// only once check says so, after the bytes check read from c. When check
// refuses, both ends are closed.
func relayChecked(c, up net.Conn, check func() (prefix []byte, ok bool)) {
	down := make(chan struct{})
	go func() {
		io.Copy(c, up)
		if cw, ok := c.(closeWriter); ok {
			cw.CloseWrite()
		} else {
			c.Close()
		}
		close(down)
	}()
	prefix, ok := check()
	if !ok {
		up.Close()
		c.Close()
		<-down
		return
	}
	if len(prefix) > 0 {
		up.Write(prefix)
	}
	io.Copy(up, c)
	if cw, ok := up.(closeWriter); ok {
		cw.CloseWrite()
	} else {
		up.Close()
	}
	<-down
}

// allowed reports whether a Permit reason allows.
func allowed(r policy.Reason) bool {
	return r == policy.ReasonAllowlisted || r == policy.ReasonInternetFull || r == policy.ReasonLANFull
}

// usesAllowlist: some zone of w is an allowlist, so TLS server names count.
func usesAllowlist(w *config.Workload) bool {
	return w.Internet == config.InternetAllowlist || w.LAN == config.InternetAllowlist
}
