package policy

import (
	"net/netip"
	"strings"

	"github.com/leroux/torii-qubes/proxy/internal/config"
)

// Zone is where an address is. Every address is in exactly one zone, and a
// workload's outbound access is decided per zone, by the address actually
// dialled: a name allowed on the internet that resolves to the LAN needs
// the LAN to allow it too.
type Zone string

const (
	ZoneForbidden Zone = "forbidden" // never dialled: loopback, link-local, multicast, Qubes' own addresses
	ZoneTailnet   Zone = "tailnet"   // 100.64/10, fd7a:115c:a1e0::/48: only through an identity
	ZoneLAN       Zone = "lan"       // private ranges of this machine's network
	ZoneInternet  Zone = "internet"  // everything else
)

var (
	// Qubes' own addresses: qubes (10.137/16), disposables (10.138/16),
	// virtual DNS and gateways (10.139/16). Other qubes are reached over
	// qrexec, as local-inbound declares, never over the network.
	qubesNets = []netip.Prefix{
		netip.MustParsePrefix("10.137.0.0/16"),
		netip.MustParsePrefix("10.138.0.0/16"),
		netip.MustParsePrefix("10.139.0.0/16"),
	}
	lanNets = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("fc00::/7"),
	}
)

// ZoneOf places an address. The tailnet's ULA is inside fc00::/7 and is
// tested first.
//
//charter:realizes SW-261127-WNYB
func ZoneOf(a netip.Addr) Zone {
	a = a.Unmap()
	if cgnat.Contains(a) || tsULA.Contains(a) {
		return ZoneTailnet
	}
	if Forbidden(a) {
		return ZoneForbidden
	}
	for _, p := range qubesNets {
		if p.Contains(a) {
			return ZoneForbidden
		}
	}
	for _, p := range lanNets {
		if p.Contains(a) {
			return ZoneLAN
		}
	}
	return ZoneInternet
}

// zoneRule is a workload's access to one zone.
func zoneRule(w *config.Workload, z Zone) (config.Internet, []string) {
	if z == ZoneLAN {
		return w.LAN, w.LANAllow
	}
	return w.Internet, w.InternetAllow
}

// Permit decides one destination outside the tailnet: an address and
// port, the names it was reached by (asked for, or resolved through the
// program's DNS), and the TLS server name if one was sent. A name entry
// matches by name (and the server name must match a name entry too); an
// address entry matches by address, whatever the names.
//
//charter:realizes SW-261127-2R9T
func Permit(w *config.Workload, names []string, a netip.Addr, p uint16, proto, sni string) (Reason, string) {
	z := ZoneOf(a)
	switch z {
	case ZoneForbidden, ZoneTailnet:
		return ReasonForbiddenAddress, ""
	}
	mode, list := zoneRule(w, z)
	switch mode {
	case config.InternetFull:
		if z == ZoneLAN {
			return ReasonLANFull, ""
		}
		return ReasonInternetFull, ""
	case config.InternetAllowlist:
	default:
		if z == ZoneLAN {
			return ReasonLANNone, ""
		}
		return ReasonInternetNone, ""
	}
	a = a.Unmap()
	var byName string
	for _, s := range list {
		e, err := config.ParseAllow(s)
		if err != nil || !entryOK(e, p, proto) {
			continue
		}
		if e.Prefix.IsValid() {
			if e.Prefix.Contains(a) {
				return ReasonAllowlisted, ""
			}
			continue
		}
		for _, n := range names {
			if Match([]string{e.Host}, n) {
				byName = n
				break
			}
		}
	}
	if byName == "" {
		if len(names) == 0 {
			return ReasonUnresolvedIP, ""
		}
		return ReasonNotAllowlisted, ""
	}
	if sni != "" && !nameEntryMatches(list, sni, p, proto) {
		return ReasonSNINotAllowed, ""
	}
	return ReasonAllowlisted, byName
}

func nameEntryMatches(list []string, name string, p uint16, proto string) bool {
	for _, s := range list {
		e, err := config.ParseAllow(s)
		if err != nil || e.Host == "" || !entryOK(e, p, proto) {
			continue
		}
		if Match([]string{e.Host}, name) {
			return true
		}
	}
	return false
}

// MayAskName reports whether a workload may ask for a name at all, before
// it is resolved: some zone allows everything, or a name entry matches it.
// Whether the connection is allowed is decided again per resolved address.
func MayAskName(w *config.Workload, name string, p uint16, proto string) (bool, Reason) {
	if w.Internet == config.InternetFull || w.LAN == config.InternetFull {
		return true, ReasonInternetFull
	}
	if nameEntryMatches(w.InternetAllow, name, p, proto) || nameEntryMatches(w.LANAllow, name, p, proto) {
		return true, ReasonAllowlisted
	}
	if (w.Internet == "" || w.Internet == config.InternetNone) && (w.LAN == "" || w.LAN == config.InternetNone) {
		return false, ReasonInternetNone
	}
	return false, ReasonNotAllowlisted
}

// PathFor is the path an allowed address takes: the LAN is this machine's
// network, always direct; the internet goes through the workload's egress
// if it has one.
func PathFor(w *config.Workload, a netip.Addr) Path {
	if ZoneOf(a) == ZoneInternet && w.Egress != "" {
		return PathExit
	}
	return PathInternet
}

// nameDecision decides a destination known only by name(s), before it is
// resolved: some zone allows everything, or a name entry matches one of the
// names; a TLS server name, when sent, must match a name entry too unless a
// zone allows everything. The resolved addresses are decided again by Permit.
func nameDecision(w *config.Workload, names []string, p uint16, proto, sni string) (Reason, string) {
	full := w.Internet == config.InternetFull || w.LAN == config.InternetFull
	host := ""
	for _, n := range names {
		if full || nameEntryMatches(w.InternetAllow, n, p, proto) || nameEntryMatches(w.LANAllow, n, p, proto) {
			host = n
			break
		}
	}
	if host == "" {
		switch {
		case len(names) == 0:
			return ReasonUnresolvedIP, ""
		case (w.Internet == "" || w.Internet == config.InternetNone) && (w.LAN == "" || w.LAN == config.InternetNone):
			return ReasonInternetNone, ""
		}
		return ReasonNotAllowlisted, ""
	}
	if sni != "" && !full && !nameEntryMatches(w.InternetAllow, sni, p, proto) && !nameEntryMatches(w.LANAllow, sni, p, proto) {
		return ReasonSNINotAllowed, ""
	}
	if full {
		return ReasonInternetFull, host
	}
	return ReasonAllowlisted, host
}

// tailnetName reports whether name (as asked, resolved, or a device's own)
// is the device an entry names: the short name itself, or a full MagicDNS
// name under it. Only called for destinations already known to be on the
// tailnet, so "notes.<anything>" here is the device notes.
func tailnetName(entry, name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	return name == entry || strings.HasPrefix(name, entry+".")
}

// TailnetPermit decides a tailnet destination against a tailnet allowlist.
// A name entry stands for the whole device: its short and full names and
// every address it owns (on a tailnet an address belongs to one device).
// An address or range entry matches the address dialled. A TLS server name,
// when sent, must name a listed device and, when the device behind the
// address is known (peer: its names), that device. Grants still decide on top.
func TailnetPermit(list []string, names, peer []string, a netip.Addr, p uint16, proto, sni string) Reason {
	names = append(append([]string{}, names...), peer...)
	byName := false
	for _, s := range list {
		e, err := config.ParseAllow(s)
		if err != nil || !entryOK(e, p, proto) {
			continue
		}
		if e.Prefix.IsValid() {
			if a.IsValid() && e.Prefix.Contains(a.Unmap()) {
				return ReasonAllowlisted
			}
			continue
		}
		for _, n := range names {
			if tailnetName(e.Host, n) {
				byName = true
			}
		}
	}
	if !byName {
		return ReasonTailnetNotListed
	}
	if sni != "" {
		if len(peer) > 0 {
			own := false
			for _, n := range peer {
				if strings.EqualFold(strings.TrimSuffix(sni, "."), n) {
					own = true
				}
			}
			if !own {
				return ReasonSNINotAllowed
			}
		}
		for _, s := range list {
			if e, err := config.ParseAllow(s); err == nil && e.Host != "" && tailnetName(e.Host, sni) &&
				entryOK(e, p, proto) {
				return ReasonAllowlisted
			}
		}
		return ReasonSNINotAllowed
	}
	return ReasonAllowlisted
}

// entryOK: the entry's port and protocol admit this connection. Port 0
// and protocol "" are "any" (a DNS question, which names no port).
//
//charter:realizes SW-261127-2XS6
func entryOK(e config.AllowEntry, p uint16, proto string) bool {
	return (e.Port == 0 || p == 0 || e.Port == p) && (proto == "" || e.Proto == proto)
}
