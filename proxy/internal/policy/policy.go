// Package policy decides each connection: allow or refuse, why, and which
// path carries it. It is pure: the caller classifies the destination
// (tailnet or not) and supplies what DNS and the TLS server name said.
package policy

import (
	"net/netip"
	"strings"

	"github.com/leroux/torii-qubes/proxy/internal/config"
)

// Verdict is allow or refuse; an event may also say closed.
type Verdict string

const (
	Allow  Verdict = "allow"
	Refuse Verdict = "refuse"
	// Closed is never decided: the client went away before sending what
	// the decision needed, and none of its bytes went upstream. Events only.
	Closed Verdict = "closed"
)

// Path is what carries an allowed connection.
type Path string

const (
	PathNone     Path = ""
	PathTailnet  Path = "tailnet"  // dialled through the workload's identity
	PathInternet Path = "internet" // dialled through the proxy qube's netvm
	PathExit     Path = "exit"     // dialled through the workload's egress: its identity, its active exit node
	PathWorkload Path = "workload" // inbound: into the workload
)

// Reason says why. Tests and show read it; keep the values stable.
type Reason string

const (
	ReasonUnknownWorkload  Reason = "unknown-workload"
	ReasonWrongEntry       Reason = "wrong-entry" // e.g. a routed qube calling qrexec
	ReasonTailnetOutbound  Reason = "tailnet-outbound"
	ReasonNoTailnet        Reason = "no-tailnet-outbound"
	ReasonInternetFull     Reason = "internet-full"
	ReasonInternetNone     Reason = "no-internet"
	ReasonLANFull          Reason = "lan-all"
	ReasonLANNone          Reason = "no-lan"
	ReasonAllowlisted      Reason = "allowlisted"
	ReasonNotAllowlisted   Reason = "not-allowlisted"
	ReasonUnresolvedIP     Reason = "ip-not-resolved-by-workload"
	ReasonSNINotAllowed    Reason = "server-name-not-allowlisted"
	ReasonSNIUnreadable    Reason = "server-name-unreadable" // TLS, but no ClientHello could be read
	ReasonClosedBeforeData Reason = "closed-before-data"     // closed (or failed) before its first bytes
	ReasonForbiddenAddress Reason = "forbidden-address"
	ReasonInboundPort      Reason = "inbound-port"
	ReasonNoInboundPort    Reason = "inbound-port-not-declared"
	ReasonUnknownForward   Reason = "unknown-forward"
	ReasonNoSOCKS          Reason = "socks-not-declared"
	ReasonNoHTTP           Reason = "http-not-declared"
	ReasonHostChanged      Reason = "http-host-changed"
	ReasonTailnetName      Reason = "tailnet-name"
	ReasonTailnetNotListed Reason = "tailnet-not-allowlisted"
	ReasonUDPFlowLimit     Reason = "udp-flow-limit"
	ReasonNoHealthyExit    Reason = "egress-no-healthy-exit" // the engine's: its egress has no exit node up
	// An egress's own events (direction egress): its exit node changed,
	// or one went down or came back.
	ReasonExitSwitch    Reason = "exit-switch"
	ReasonExitUnhealthy Reason = "exit-unhealthy"
	ReasonExitHealthy   Reason = "exit-healthy"
)

// Entry is how an outbound connection arrived.
type Entry string

const (
	EntryIntercept Entry = "intercept" // routed workload, transparent
	EntrySOCKS     Entry = "socks"     // qrexec workload or proxy qube, SOCKS port
	EntryForward   Entry = "forward"   // qrexec workload or proxy qube, fixed forward
	EntryHTTP      Entry = "http"      // qrexec workload, HTTP proxy
)

// Outbound is one outbound connection, classified.
type Outbound struct {
	Workload string
	Entry    Entry
	// Host is the name the workload asked for (SOCKS, forward), if any.
	Host string
	// Addr is the destination address, if known (intercept, or SOCKS
	// with an address).
	Addr netip.Addr
	Port uint16
	// Tailnet is true when the destination is on the tailnet: its address
	// is a tailnet route of the workload's identity, or its name is a
	// tailnet name. The caller decides, using the identity.
	Tailnet bool
	// ResolvedFrom lists the names this workload resolved to Addr through
	// the program's DNS, for intercepted connections.
	ResolvedFrom []string
	// SNI is the TLS server name the workload sent, if the connection
	// opened with a ClientHello.
	SNI string
	// UDP marks a UDP flow; allowlist entries are TCP unless they say /udp.
	UDP bool
	// PeerNames is the names of the tailnet device that owns Addr, for a
	// tailnet destination (the caller asks the identity).
	PeerNames []string
}

// Decision is what the program does with a connection.
type Decision struct {
	Verdict  Verdict `json:"verdict"`
	Reason   Reason  `json:"reason"`
	Path     Path    `json:"path,omitempty"`
	Identity string  `json:"identity,omitempty"` // tailnet hostname used
	// Host is the name the decision rested on, if any.
	Host string `json:"host,omitempty"`
}

func refuse(r Reason) Decision { return Decision{Verdict: Refuse, Reason: r} }

func (o Outbound) proto() string {
	if o.UDP {
		return "udp"
	}
	return "tcp"
}

// DecideOutbound decides an outbound connection. A tailnet destination is
// dialled only on the workload's own identity, never its egress's.
//
//charter:realizes SW-261127-PXX9, SW-261127-WSWG
func DecideOutbound(c *config.Config, o Outbound) Decision {
	w, ok := c.Lookup(o.Workload)
	if !ok {
		return refuse(ReasonUnknownWorkload)
	}
	switch o.Entry {
	case EntryIntercept:
		if w.Link != config.LinkRouted {
			return refuse(ReasonWrongEntry)
		}
	case EntrySOCKS, EntryForward, EntryHTTP:
		if w.Link == config.LinkRouted {
			return refuse(ReasonWrongEntry)
		}
		if o.Entry == EntrySOCKS && !w.SOCKS {
			return refuse(ReasonNoSOCKS)
		}
		if o.Entry == EntryHTTP && !w.HTTP {
			return refuse(ReasonNoHTTP)
		}
	default:
		return refuse(ReasonWrongEntry)
	}
	ident := ""
	if w.Identity != nil {
		ident = w.Identity.Hostname
	}
	if !o.Addr.IsValid() {
		if a, err := netip.ParseAddr(o.Host); err == nil {
			o.Addr, o.Host = a.Unmap(), ""
		}
	}
	if o.Tailnet {
		if !w.TailnetOutbound {
			return refuse(ReasonNoTailnet)
		}
		if len(w.TailnetAllow) > 0 {
			names := append([]string{}, o.ResolvedFrom...)
			if o.Host != "" {
				names = append(names, o.Host)
			}
			if r := TailnetPermit(w.TailnetAllow, names, o.PeerNames, o.Addr, o.Port, o.proto(), o.SNI); r != ReasonAllowlisted {
				return refuse(r)
			}
		}
		return Decision{Verdict: Allow, Reason: ReasonTailnetOutbound, Path: PathTailnet, Identity: ident, Host: o.Host}
	}
	// Outside the tailnet: decided per zone, by the address dialled.
	if o.Addr.IsValid() {
		names := o.ResolvedFrom
		if o.Host != "" {
			if _, err := netip.ParseAddr(o.Host); err != nil {
				names = []string{o.Host}
			}
		}
		r, host := Permit(w, names, o.Addr, o.Port, o.proto(), o.SNI)
		if r != ReasonAllowlisted && r != ReasonInternetFull && r != ReasonLANFull {
			return refuse(r)
		}
		path := PathFor(w, o.Addr)
		ident = pathIdentity(c, w, path, ident)
		if host == "" {
			host = o.Host
		}
		return Decision{Verdict: Allow, Reason: r, Path: path, Identity: ident, Host: host}
	}
	// A name not yet resolved: allowed if some zone could allow it. The
	// engine resolves it and dials only addresses Permit allows.
	names := o.ResolvedFrom
	if o.Host != "" {
		names = []string{o.Host}
	}
	r, host := nameDecision(w, names, o.Port, o.proto(), o.SNI)
	if r != ReasonAllowlisted && r != ReasonInternetFull {
		return refuse(r)
	}
	o.Host = host
	path := PathInternet
	if w.Egress != "" {
		path = PathExit
	}
	ident = pathIdentity(c, w, path, ident)
	return Decision{Verdict: Allow, Reason: r, Path: path, Identity: ident, Host: o.Host}
}

// Match reports whether host matches an allowlist. "example.com" matches
// only itself; "*.example.com" matches every name under example.com but
// not example.com itself.
//
//charter:realizes SW-261127-2XS6
func Match(allow []string, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	for _, p := range allow {
		if suffix, ok := strings.CutPrefix(p, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == p {
			return true
		}
	}
	return false
}

// DecideName decides a DNS question from a routed workload: whether the
// name may be answered, and through which path it is looked up. tailnet
// says the name is a tailnet name of the workload's identity.
func DecideName(c *config.Config, workload, name string, tailnet bool) Decision {
	w, ok := c.Lookup(workload)
	if !ok {
		return refuse(ReasonUnknownWorkload)
	}
	if tailnet {
		if !w.TailnetOutbound {
			return refuse(ReasonNoTailnet)
		}
		if len(w.TailnetAllow) > 0 && TailnetPermit(w.TailnetAllow, []string{name}, nil, netip.Addr{}, 0, "", "") != ReasonAllowlisted {
			return refuse(ReasonTailnetNotListed)
		}
		return Decision{Verdict: Allow, Reason: ReasonTailnetName, Path: PathTailnet, Identity: w.Identity.Hostname, Host: name}
	}
	path := PathInternet
	if w.Egress != "" {
		path = PathExit
	}
	ident := pathIdentity(c, w, path, "")
	if ok, r := MayAskName(w, name, 0, ""); ok {
		return Decision{Verdict: Allow, Reason: r, Path: path, Identity: ident, Host: name}
	} else {
		return refuse(r)
	}
}

// pathIdentity is the identity an internet-zone path uses: the egress's
// on the exit path, none direct. The workload's own identity (own) is for
// the tailnet only.
//
//charter:realizes SW-261127-WSWG
func pathIdentity(c *config.Config, w *config.Workload, path Path, own string) string {
	switch path {
	case PathExit:
		if g := c.EgressOf(w); g != nil {
			return g.Hostname
		}
		return ""
	case PathTailnet:
		return own
	}
	return ""
}

// DecideInbound decides a connection arriving at a workload's identity.
//
//charter:realizes SW-261127-T7GG
func DecideInbound(c *config.Config, workload string, port uint16) (Decision, uint16) {
	w, ok := c.Workloads[workload]
	if !ok || w.Identity == nil {
		return refuse(ReasonUnknownWorkload), 0
	}
	for _, in := range w.Inbound {
		if in.Port == port {
			return Decision{Verdict: Allow, Reason: ReasonInboundPort, Path: PathWorkload, Identity: w.Identity.Hostname}, in.TargetPort()
		}
	}
	return refuse(ReasonNoInboundPort), 0
}

// ForwardTarget returns a qrexec workload's fixed forward by name.
func ForwardTarget(c *config.Config, workload, name string) (string, bool) {
	w, ok := c.Workloads[workload]
	if !ok {
		return "", false
	}
	for _, f := range w.Forwards {
		if f.Name == name {
			return f.To, true
		}
	}
	return "", false
}

var (
	cgnat    = netip.MustParsePrefix("100.64.0.0/10")
	tsULA    = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	thisNet  = netip.MustParsePrefix("0.0.0.0/8")
	qubesDNS = netip.MustParsePrefix("10.139.1.0/24")
)

// Forbidden reports addresses never dialled on the internet path:
// loopback, link-local, multicast, unspecified, the proxy qube's own
// Qubes virtual DNS, and the tailnet's ranges, which must go through an
// identity or not at all (tsnet would otherwise dial them on the system
// network).
func Forbidden(a netip.Addr) bool {
	a = a.Unmap()
	return !a.IsValid() || a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() ||
		cgnat.Contains(a) || tsULA.Contains(a) || thisNet.Contains(a) || qubesDNS.Contains(a) ||
		a == netip.AddrFrom4([4]byte{255, 255, 255, 255})
}
