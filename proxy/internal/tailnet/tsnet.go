package tailnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
	"tailscale.com/wgengine/netstack"
)

// TSNetOptions configures a tsnet identity.
type TSNetOptions struct {
	Hostname   string
	Dir        string // state folder, kept on the private volume
	ControlURL string
	Tags       []string
	AuthKey    string // empty: sign in by link
	Ephemeral  bool
	Logf       logger.Logf
	// Store, when set, replaces the file store (tests use mem.Store).
	Store ipn.StateStore
}

// TSNet is an Identity backed by a tsnet.Server.
type TSNet struct {
	opt TSNetOptions
	srv *tsnet.Server
	lc  *local.Client

	mu       sync.Mutex
	state    string
	authURL  string
	exitNode string
	lastErr  string
	stCache  *ipnstate.Status
	stAt     time.Time
	cancel   context.CancelFunc
}

// NewTSNet makes an identity; Start joins it.
func NewTSNet(opt TSNetOptions) *TSNet {
	logf := opt.Logf
	if logf == nil {
		logf = logger.Discard
	}
	// With a key, the key carries the tags: Headscale refuses advertised
	// tags on key registrations, and Tailscale takes the key's. Without
	// one, the tags are advertised and the approving admin must own them.
	advertise := opt.Tags
	if opt.AuthKey != "" {
		advertise = nil
	}
	return &TSNet{
		opt:   opt,
		state: "NoState",
		srv: &tsnet.Server{
			Dir:           opt.Dir,
			Hostname:      opt.Hostname,
			ControlURL:    opt.ControlURL,
			AdvertiseTags: advertise,
			AuthKey:       opt.AuthKey,
			Ephemeral:     opt.Ephemeral,
			Store:         opt.Store,
			Logf:          logf,
			UserLogf:      logf,
		},
	}
}

// Server exposes the tsnet server, for tests that need its internals.
func (t *TSNet) Server() *tsnet.Server { return t.srv }

func (t *TSNet) Hostname() string { return t.opt.Hostname }

// StateDir is where the identity keeps its state.
func (t *TSNet) StateDir() string { return t.opt.Dir }

func (t *TSNet) Start(ctx context.Context) error {
	if t.opt.Dir != "" {
		if err := os.MkdirAll(t.opt.Dir, 0o700); err != nil {
			return err
		}
	}
	if err := t.srv.Start(); err != nil {
		return err
	}
	lc, err := t.srv.LocalClient()
	if err != nil {
		return err
	}
	t.lc = lc
	wctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	w, err := lc.WatchIPNBus(wctx, ipn.NotifyInitialState)
	if err != nil {
		cancel()
		return err
	}
	go func() {
		defer w.Close()
		for {
			n, err := w.Next()
			if err != nil {
				return
			}
			t.mu.Lock()
			if n.State != nil {
				t.state = n.State.String()
				if *n.State == ipn.Running {
					t.authURL = ""
				}
			}
			if n.BrowseToURL != nil {
				t.authURL = *n.BrowseToURL
			}
			if n.ErrMessage != nil {
				t.lastErr = *n.ErrMessage
			}
			t.stCache = nil
			t.mu.Unlock()
		}
	}()
	return nil
}

func (t *TSNet) AwaitRunning(ctx context.Context) error {
	for {
		t.mu.Lock()
		st := t.state
		t.mu.Unlock()
		if st == ipn.Running.String() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w (state %s)", t.opt.Hostname, ctx.Err(), st)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (t *TSNet) netstack() (*netstack.Impl, error) {
	ns, ok := t.srv.Sys().Netstack.GetOK()
	if !ok {
		return nil, errors.New("tailnet: not started")
	}
	impl, ok := ns.(*netstack.Impl)
	if !ok {
		return nil, errors.New("tailnet: unexpected netstack")
	}
	return impl, nil
}

// Dial goes straight into the identity's netstack, bound to its own
// address, so nothing reaches the system network: without a peer or an
// exit node for dst, WireGuard has nowhere to send the packets and the
// dial fails (fails closed).
//
//charter:realizes SW-261127-K5TK
func (t *TSNet) Dial(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	ns, err := t.netstack()
	if err != nil {
		return nil, err
	}
	v4, v6 := t.srv.TailscaleIPs()
	src := v4
	if dst.Addr().Is6() {
		src = v6
	}
	if !src.IsValid() {
		return nil, fmt.Errorf("%w: no address of that family", ErrNoRoute)
	}
	c, err := ns.DialContextTCPWithBind(ctx, src, dst)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (t *TSNet) srcFor(dst netip.AddrPort) (netip.Addr, error) {
	v4, v6 := t.srv.TailscaleIPs()
	src := v4
	if dst.Addr().Is6() {
		src = v6
	}
	if !src.IsValid() {
		return src, fmt.Errorf("%w: no address of that family", ErrNoRoute)
	}
	return src, nil
}

// DialUDP is Dial for UDP: into the netstack, bound to the identity.
//
//charter:realizes SW-261127-K5TK
func (t *TSNet) DialUDP(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	ns, err := t.netstack()
	if err != nil {
		return nil, err
	}
	src, err := t.srcFor(dst)
	if err != nil {
		return nil, err
	}
	c, err := ns.DialContextUDPWithBind(ctx, src, dst)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (t *TSNet) ListenUDP(port uint16) (net.PacketConn, error) {
	v4, _ := t.srv.TailscaleIPs()
	return t.srv.ListenPacket("udp", netip.AddrPortFrom(v4, port).String())
}

func (t *TSNet) status(ctx context.Context) (*ipnstate.Status, error) {
	t.mu.Lock()
	if t.stCache != nil && time.Since(t.stAt) < 2*time.Second {
		st := t.stCache
		t.mu.Unlock()
		return st, nil
	}
	t.mu.Unlock()
	if t.lc == nil {
		return nil, errors.New("tailnet: not started")
	}
	st, err := t.lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.stCache, t.stAt = st, time.Now()
	t.mu.Unlock()
	return st, nil
}

func (t *TSNet) IsTailnet(ctx context.Context, a netip.Addr) bool {
	st, err := t.status(ctx)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range st.Peer {
		for _, ip := range p.TailscaleIPs {
			if ip == a {
				return true
			}
		}
		if p.PrimaryRoutes != nil {
			for _, r := range p.PrimaryRoutes.All() {
				if tsaddr.IsExitRoute(r) {
					continue
				}
				if r.Contains(a) {
					return true
				}
			}
		}
	}
	return false
}

func (t *TSNet) LookupTailnet(ctx context.Context, name string) ([]netip.Addr, bool) {
	st, err := t.status(ctx)
	if err != nil {
		return nil, false
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	suffix := ""
	if st.CurrentTailnet != nil {
		suffix = strings.ToLower(st.CurrentTailnet.MagicDNSSuffix)
	}
	isTailnet := suffix != "" && strings.HasSuffix(name, "."+suffix)
	for _, p := range st.Peer {
		fqdn := strings.TrimSuffix(strings.ToLower(p.DNSName), ".")
		short, _, _ := strings.Cut(fqdn, ".")
		if name == fqdn || (!strings.Contains(name, ".") && name == short) {
			return append([]netip.Addr(nil), p.TailscaleIPs...), true
		}
	}
	return nil, isTailnet
}

func (t *TSNet) PeerNames(ctx context.Context, a netip.Addr) []string {
	st, err := t.status(ctx)
	if err != nil {
		return nil
	}
	a = a.Unmap()
	for _, p := range st.Peer {
		for _, ip := range p.TailscaleIPs {
			if ip == a {
				fqdn := strings.TrimSuffix(strings.ToLower(p.DNSName), ".")
				short, _, _ := strings.Cut(fqdn, ".")
				return []string{short, fqdn}
			}
		}
	}
	return nil
}

func (t *TSNet) Listen(port uint16) (net.Listener, error) {
	return t.srv.Listen("tcp", fmt.Sprintf(":%d", port))
}

func (t *TSNet) ListenTLS(port uint16) (net.Listener, error) {
	return t.srv.ListenTLS("tcp", fmt.Sprintf(":%d", port))
}

func (t *TSNet) WhoIs(ctx context.Context, remote string) string {
	if t.lc == nil {
		return remote
	}
	w, err := t.lc.WhoIs(ctx, remote)
	if err != nil || w.Node == nil {
		return remote
	}
	name := strings.TrimSuffix(w.Node.Name, ".")
	if w.UserProfile != nil && w.UserProfile.LoginName != "" && !w.Node.IsTagged() {
		return name + " (" + w.UserProfile.LoginName + ")"
	}
	return name
}

// findPeer is the peer a node names: its short or full MagicDNS name, its
// hostname, or one of its addresses.
func findPeer(st *ipnstate.Status, node string) *ipnstate.PeerStatus {
	want := strings.TrimSuffix(strings.ToLower(node), ".")
	wantIP, _ := netip.ParseAddr(want)
	for _, p := range st.Peer {
		fqdn := strings.TrimSuffix(strings.ToLower(p.DNSName), ".")
		short, _, _ := strings.Cut(fqdn, ".")
		if want == fqdn || want == short || strings.EqualFold(p.HostName, want) {
			return p
		}
		for _, ip := range p.TailscaleIPs {
			if ip == wantIP {
				return p
			}
		}
	}
	return nil
}

func (t *TSNet) ExitPeer(ctx context.Context, node string) (Peer, error) {
	st, err := t.lc.Status(ctx)
	if err != nil {
		return Peer{}, err
	}
	p := findPeer(st, node)
	if p == nil {
		return Peer{}, nil
	}
	return Peer{Found: true, Online: p.Online, OffersExit: p.ExitNodeOption}, nil
}

func (t *TSNet) Ping(ctx context.Context, node string) error {
	st, err := t.lc.Status(ctx)
	if err != nil {
		return err
	}
	p := findPeer(st, node)
	if p == nil || len(p.TailscaleIPs) == 0 {
		return fmt.Errorf("tailnet: %s not found", node)
	}
	r, err := t.lc.Ping(ctx, p.TailscaleIPs[0], tailcfg.PingTSMP)
	if err != nil {
		return err
	}
	if r.Err != "" {
		return errors.New(r.Err)
	}
	return nil
}

// SetExitNode names node as the exit node in one prefs change, LAN access
// through it off; it never clears the exit node.
//
//charter:realizes SW-261127-6YGC
func (t *TSNet) SetExitNode(ctx context.Context, node string) error {
	st, err := t.lc.Status(ctx)
	if err != nil {
		return err
	}
	p := findPeer(st, node)
	if p == nil {
		return fmt.Errorf("tailnet: exit node %s not found", node)
	}
	if !p.ExitNodeOption {
		return fmt.Errorf("tailnet: %s does not offer an exit node", node)
	}
	// One prefs change, from the old node to the new: there is never a
	// moment with no exit node set.
	_, err = t.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:                     ipn.Prefs{ExitNodeID: p.ID, ExitNodeAllowLANAccess: false},
		ExitNodeIDSet:             true,
		ExitNodeAllowLANAccessSet: true,
	})
	if err == nil {
		t.mu.Lock()
		t.exitNode = strings.TrimSuffix(strings.ToLower(p.DNSName), ".")
		t.stCache = nil
		t.mu.Unlock()
	}
	return err
}

func (t *TSNet) Status(ctx context.Context) Status {
	t.mu.Lock()
	s := Status{Hostname: t.opt.Hostname, State: t.state, AuthURL: t.authURL, ExitNode: t.exitNode, Err: t.lastErr}
	t.mu.Unlock()
	if st, err := t.status(ctx); err == nil {
		for _, ip := range st.TailscaleIPs {
			s.IPs = append(s.IPs, ip.String())
		}
		s.Peers = len(st.Peer)
		if st.BackendState == ipn.Running.String() && st.Self != nil {
			var got []string
			if st.Self.Tags != nil {
				got = st.Self.Tags.AsSlice()
			}
			s.Tags = got
			if !sameSet(got, t.opt.Tags) {
				s.Err = fmt.Sprintf("identity has tags %v, config says %v", got, t.opt.Tags)
			}
		}
		if s.AuthURL == "" && st.AuthURL != "" && st.BackendState != ipn.Running.String() {
			s.AuthURL = st.AuthURL
		}
	}
	return s
}

func (t *TSNet) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	return t.srv.Close()
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}
