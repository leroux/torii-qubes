// Package tailnet holds the workloads' tailnet identities: one tsnet node
// per workload, all in one process. Identity is the seam: the engine
// uses only it, so tests swap in Fake.
package tailnet

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

// Identity is one workload's node on the tailnet.
type Identity interface {
	// Hostname is the identity's tailnet hostname.
	Hostname() string
	// Start joins the tailnet without waiting for sign-in.
	Start(ctx context.Context) error
	// AwaitRunning waits until the identity is signed in and running.
	AwaitRunning(ctx context.Context) error
	// Dial opens a TCP connection through the identity's WireGuard
	// tunnel. It never falls back to the system network: an address with
	// no tailnet route, and no exit node to carry it, fails.
	Dial(ctx context.Context, dst netip.AddrPort) (net.Conn, error)
	// DialUDP opens a connected UDP socket through the tunnel, with the
	// same rule: no tailnet route and no exit node, no packets out.
	DialUDP(ctx context.Context, dst netip.AddrPort) (net.Conn, error)
	// ListenUDP listens on a UDP port of the identity.
	ListenUDP(port uint16) (net.PacketConn, error)
	// IsTailnet reports whether a is a peer's address or a subnet route
	// on the tailnet (exit-node routes excluded).
	IsTailnet(ctx context.Context, a netip.Addr) bool
	// LookupTailnet resolves a tailnet name (a MagicDNS FQDN or a peer's
	// short name). ok is false when the name is not a tailnet name; a
	// tailnet name with no such peer returns ok and no addresses.
	LookupTailnet(ctx context.Context, name string) (addrs []netip.Addr, ok bool)
	// PeerNames is the short and full MagicDNS names of the tailnet device
	// that owns address a, or nil. On a tailnet an address belongs to one
	// device, so a name stands for all of that device's addresses.
	PeerNames(ctx context.Context, a netip.Addr) []string
	// Listen listens on a TCP port of the identity.
	Listen(port uint16) (net.Listener, error)
	// ListenTLS listens on a TCP port and terminates TLS with the
	// identity's own certificate for its MagicDNS name (the tailnet must
	// have HTTPS certificates enabled).
	ListenTLS(port uint16) (net.Listener, error)
	// WhoIs names the tailnet peer behind a remote address.
	WhoIs(ctx context.Context, remote string) string
	// SetExitNode selects the exit node that carries the identity's
	// internet connections, by hostname, FQDN or address.
	SetExitNode(ctx context.Context, node string) error
	// Status is a snapshot for status and show.
	Status(ctx context.Context) Status
	Close() error
}

// Status of one identity.
type Status struct {
	Hostname string   `json:"hostname"`
	State    string   `json:"state"`              // NeedsLogin, Starting, Running, ...
	AuthURL  string   `json:"auth_url,omitempty"` // sign-in link to relay
	IPs      []string `json:"ips,omitempty"`
	Tags     []string `json:"tags,omitempty"` // as the coordination server assigned them
	ExitNode string   `json:"exit_node,omitempty"`
	Peers    int      `json:"peers"`
	Err      string   `json:"error,omitempty"`
}

// ErrNoRoute: the address is not reachable through this identity.
var ErrNoRoute = errors.New("tailnet: no route through this identity")
