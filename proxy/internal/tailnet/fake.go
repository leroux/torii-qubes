package tailnet

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"

	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
)

// FakeNet is an in-memory tailnet: identities on it dial each other's
// listeners through net.Pipe. Grants are not modelled; reachability is
// all-to-all unless Deny says otherwise. Exit nodes relay to Internet.
type FakeNet struct {
	Suffix string // MagicDNS suffix, default "fake.ts.net"
	// Internet is what exit nodes dial out with.
	Internet func(ctx context.Context, dst netip.AddrPort) (net.Conn, error)
	// InternetUDP is what exit nodes send UDP out with.
	InternetUDP func(ctx context.Context, dst netip.AddrPort) (net.Conn, error)
	// Deny, when set, refuses connections from one hostname to another.
	Deny func(from, to string, port uint16) bool

	mu    sync.Mutex
	next  byte
	nodes map[string]*Fake
	lns   map[netip.AddrPort]*fakeListener
	udp   map[netip.AddrPort]string // tailnet address -> loopback UDP address
}

// Fake is an identity on a FakeNet.
type Fake struct {
	net      *FakeNet
	hostname string
	ip       netip.Addr

	mu        sync.Mutex
	running   bool
	authURL   string
	exit      string
	offerExit bool
	offline   bool
	silent    bool // listed online, but nothing reaches it
	uplink    bool // as an exit node: its own internet is down
	closed    bool
}

// Node adds (or returns) an identity. It is running at once unless
// NeedsLogin is called before Start.
func (n *FakeNet) Node(hostname string) *Fake {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.nodes == nil {
		n.nodes = map[string]*Fake{}
		n.lns = map[netip.AddrPort]*fakeListener{}
		n.udp = map[netip.AddrPort]string{}
	}
	if f, ok := n.nodes[hostname]; ok {
		return f
	}
	n.next++
	f := &Fake{net: n, hostname: hostname, ip: netip.AddrFrom4([4]byte{100, 64, 0, n.next}), running: true}
	n.nodes[hostname] = f
	return f
}

func (n *FakeNet) suffix() string {
	if n.Suffix == "" {
		return "fake.ts.net"
	}
	return n.Suffix
}

// IP is the fake's tailnet address.
func (f *Fake) IP() netip.Addr { return f.ip }

// NeedsLogin makes the identity wait for Approve, showing a sign-in link.
func (f *Fake) NeedsLogin() {
	f.mu.Lock()
	f.running = false
	f.authURL = "https://login.fake/a/" + f.hostname
	f.mu.Unlock()
}

// Approve completes sign-in.
func (f *Fake) Approve() {
	f.mu.Lock()
	f.running, f.authURL = true, ""
	f.mu.Unlock()
}

// OfferExitNode makes this node an exit node.
func (f *Fake) OfferExitNode() { f.mu.Lock(); f.offerExit = true; f.mu.Unlock() }

// StopOfferingExitNode withdraws its exit route (fault injection). Traffic
// already sent through it stops being relayed.
func (f *Fake) StopOfferingExitNode() {
	f.mu.Lock()
	f.offerExit = false
	f.uplink = true
	f.mu.Unlock()
}

// SetOffline takes the node off the tailnet (fault injection).
func (f *Fake) SetOffline(off bool) { f.mu.Lock(); f.offline = off; f.mu.Unlock() }

// SetSilent leaves the node listed online but unreachable: no pings, no
// traffic (fault injection: the control server lags).
func (f *Fake) SetSilent(s bool) { f.mu.Lock(); f.silent = s; f.mu.Unlock() }

// SetUplinkDown keeps the node answering pings while, as an exit node,
// it relays nothing (fault injection: its own internet is down).
func (f *Fake) SetUplinkDown(d bool) { f.mu.Lock(); f.uplink = d; f.mu.Unlock() }

func (f *Fake) reachable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running && !f.offline && !f.closed && !f.silent
}

func (f *Fake) relays() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.uplink
}

func (f *Fake) Hostname() string                { return f.hostname }
func (f *Fake) Start(ctx context.Context) error { return nil }
func (f *Fake) up() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running && !f.offline && !f.closed
}

func (f *Fake) AwaitRunning(ctx context.Context) error {
	for !f.up() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-afterShort():
		}
	}
	return nil
}

func (f *Fake) Dial(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	if !f.up() {
		return nil, errors.New("fake: identity not running")
	}
	n := f.net
	n.mu.Lock()
	ln := n.lns[dst]
	var owner *Fake
	for _, o := range n.nodes {
		if o.ip == dst.Addr() {
			owner = o
		}
	}
	n.mu.Unlock()
	if owner != nil {
		if !owner.up() {
			return nil, fmt.Errorf("fake: %s offline", owner.hostname)
		}
		if n.Deny != nil && n.Deny(f.hostname, owner.hostname, dst.Port()) {
			return nil, errors.New("fake: refused by grants")
		}
		if ln == nil {
			return nil, errors.New("fake: connection refused")
		}
		a, b := net.Pipe()
		select {
		case ln.ch <- &addrConn{Conn: b, remote: netip.AddrPortFrom(f.ip, 40000)}:
			return &addrConn{Conn: a, remote: dst}, nil
		case <-ln.done:
			return nil, errors.New("fake: connection refused")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Not on the tailnet: only an exit node carries it, else it fails
	// closed, like WireGuard with no peer for the address.
	f.mu.Lock()
	exit := f.exit
	f.mu.Unlock()
	if exit == "" {
		return nil, ErrNoRoute
	}
	n.mu.Lock()
	en := n.nodes[exit]
	n.mu.Unlock()
	if en == nil || !en.reachable() {
		return nil, fmt.Errorf("fake: exit node %s unreachable", exit)
	}
	if !en.relays() {
		return nil, fmt.Errorf("fake: exit node %s: no route out", exit)
	}
	if n.Internet == nil {
		return nil, errors.New("fake: no internet")
	}
	return n.Internet(ctx, dst)
}

// ListenUDP backs a tailnet UDP port with a loopback socket. Datagrams
// arrive from loopback addresses: the fake does not model UDP sources.
func (f *Fake) ListenUDP(port uint16) (net.PacketConn, error) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.net.mu.Lock()
	f.net.udp[netip.AddrPortFrom(f.ip, port)] = pc.LocalAddr().String()
	f.net.mu.Unlock()
	return pc, nil
}

func (f *Fake) DialUDP(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	if !f.up() {
		return nil, errors.New("fake: identity not running")
	}
	n := f.net
	n.mu.Lock()
	real, ok := n.udp[dst]
	var owner *Fake
	for _, o := range n.nodes {
		if o.ip == dst.Addr() {
			owner = o
		}
	}
	n.mu.Unlock()
	if owner != nil {
		if !owner.up() || !ok {
			return nil, errors.New("fake: unreachable")
		}
		return net.Dial("udp", real)
	}
	f.mu.Lock()
	exit := f.exit
	f.mu.Unlock()
	if exit == "" {
		return nil, ErrNoRoute
	}
	n.mu.Lock()
	en := n.nodes[exit]
	n.mu.Unlock()
	if en == nil || !en.reachable() || !en.relays() || n.InternetUDP == nil {
		return nil, fmt.Errorf("fake: exit node %s unreachable", exit)
	}
	return n.InternetUDP(ctx, dst)
}

func (f *Fake) IsTailnet(ctx context.Context, a netip.Addr) bool {
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	for _, o := range f.net.nodes {
		if o.ip == a.Unmap() && o != f {
			return true
		}
	}
	return false
}

func (f *Fake) LookupTailnet(ctx context.Context, name string) ([]netip.Addr, bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	sfx := "." + f.net.suffix()
	short := strings.TrimSuffix(name, sfx)
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	if o, ok := f.net.nodes[short]; ok && (short != name || !strings.Contains(name, ".")) {
		return []netip.Addr{o.ip}, true
	}
	return nil, strings.HasSuffix(name, sfx)
}

func (f *Fake) PeerNames(ctx context.Context, a netip.Addr) []string {
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	for h, n := range f.net.nodes {
		if n.ip == a.Unmap() {
			return []string{h, h + "." + f.net.suffix()}
		}
	}
	return nil
}

func (f *Fake) Listen(port uint16) (net.Listener, error) {
	ap := netip.AddrPortFrom(f.ip, port)
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	if _, ok := f.net.lns[ap]; ok {
		return nil, errors.New("fake: address in use")
	}
	ln := &fakeListener{addr: ap, ch: make(chan net.Conn), done: make(chan struct{}), net: f.net}
	f.net.lns[ap] = ln
	return ln, nil
}

// ListenTLS terminates TLS with a self-signed certificate for the
// identity's name, so tests see a handshake and the server name.
func (f *Fake) ListenTLS(port uint16) (net.Listener, error) {
	ln, err := f.Listen(port)
	if err != nil {
		return nil, err
	}
	cert, err := selfSigned(f.hostname + ".fake.ts.net")
	if err != nil {
		ln.Close()
		return nil, err
	}
	return tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}}), nil
}

func selfSigned(name string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func (f *Fake) WhoIs(ctx context.Context, remote string) string {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return remote
	}
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	for _, o := range f.net.nodes {
		if o.ip == ap.Addr() {
			return o.hostname + "." + f.net.suffix()
		}
	}
	return remote
}

func (f *Fake) SetExitNode(ctx context.Context, node string) error {
	short := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(node), "."), "."+f.net.suffix())
	f.net.mu.Lock()
	en := f.net.nodes[short]
	f.net.mu.Unlock()
	if en == nil {
		return fmt.Errorf("fake: exit node %s not found", node)
	}
	en.mu.Lock()
	offer := en.offerExit
	en.mu.Unlock()
	if !offer {
		return fmt.Errorf("fake: %s does not offer an exit node", node)
	}
	f.mu.Lock()
	f.exit = short
	f.mu.Unlock()
	return nil
}

func (f *Fake) peer(node string) *Fake {
	short := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(node), "."), "."+f.net.suffix())
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	return f.net.nodes[short]
}

func (f *Fake) ExitPeer(ctx context.Context, node string) (Peer, error) {
	en := f.peer(node)
	if en == nil {
		return Peer{}, nil
	}
	en.mu.Lock()
	defer en.mu.Unlock()
	return Peer{Found: true, Online: en.running && !en.offline && !en.closed, OffersExit: en.offerExit}, nil
}

func (f *Fake) Ping(ctx context.Context, node string) error {
	if !f.up() {
		return errors.New("fake: identity not running")
	}
	en := f.peer(node)
	if en == nil {
		return fmt.Errorf("fake: %s not found", node)
	}
	if !en.reachable() {
		// As tsnet's TSMP ping does: no answer means no return, only the
		// context ending.
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *Fake) Status(ctx context.Context) Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := Status{Hostname: f.hostname, State: "Running", AuthURL: f.authURL, IPs: []string{f.ip.String()}, ExitNode: f.exit}
	if !f.running {
		st.State = "NeedsLogin"
	}
	f.net.mu.Lock()
	st.Peers = len(f.net.nodes) - 1
	f.net.mu.Unlock()
	return st
}

func (f *Fake) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.net.mu.Lock()
	for ap, ln := range f.net.lns {
		if ap.Addr() == f.ip {
			ln.closeLocked()
		}
	}
	f.net.mu.Unlock()
	return nil
}

type fakeListener struct {
	addr netip.AddrPort
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
	net  *FakeNet
}

func (l *fakeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *fakeListener) closeLocked() {
	l.once.Do(func() { close(l.done); delete(l.net.lns, l.addr) })
}
func (l *fakeListener) Close() error {
	l.net.mu.Lock()
	defer l.net.mu.Unlock()
	l.closeLocked()
	return nil
}
func (l *fakeListener) Addr() net.Addr { return net.TCPAddrFromAddrPort(l.addr) }

type addrConn struct {
	net.Conn
	remote netip.AddrPort
}

func (c *addrConn) RemoteAddr() net.Addr { return net.TCPAddrFromAddrPort(c.remote) }
