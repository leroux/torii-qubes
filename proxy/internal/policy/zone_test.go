package policy

import (
	"github.com/leroux/torii-qubes/claims"
	"net/netip"
	"testing"

	"github.com/leroux/torii-qubes/proxy/internal/config"
)

func TestZoneOf(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_WNYB, "zones-partition")
	for addr, want := range map[string]Zone{
		"93.184.215.14":      ZoneInternet,
		"2606:4700::1111":    ZoneInternet,
		"192.168.1.1":        ZoneLAN,
		"172.20.0.5":         ZoneLAN,
		"10.20.30.40":        ZoneLAN,
		"fd00::1":            ZoneLAN,
		"10.137.0.28":        ZoneForbidden, // another qube
		"10.138.39.88":       ZoneForbidden, // a disposable (sys-firewall)
		"10.139.1.1":         ZoneForbidden, // Qubes' virtual DNS
		"127.0.0.1":          ZoneForbidden,
		"169.254.1.1":        ZoneForbidden,
		"224.0.0.251":        ZoneForbidden,
		"100.101.102.103":    ZoneTailnet,
		"fd7a:115c:a1e0::1":  ZoneTailnet, // inside fc00::/7, but the tailnet's
		"::ffff:192.168.1.1": ZoneLAN,     // v4-mapped
		"::ffff:10.137.0.5":  ZoneForbidden,
		"0.1.2.3":            ZoneForbidden, // 0.0.0.0/8
		"255.255.255.255":    ZoneForbidden, // broadcast
	} {
		if got := ZoneOf(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %s, want %s", addr, got, want)
		}
	}
}

func wl(internet config.Internet, ia []string, lan config.Internet, la []string) *config.Workload {
	return &config.Workload{Link: config.LinkRouted, Internet: internet, InternetAllow: ia, LAN: lan, LANAllow: la}
}

func TestPermit(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_WNYB, "other-qube-forbidden", "internet-name-to-lan")
	a := netip.MustParseAddr
	full := wl(config.InternetFull, nil, config.InternetNone, nil)
	list := wl(config.InternetAllowlist, []string{"pypi.org", "*.hf.co:443", "93.184.215.0/24:80"},
		config.InternetAllowlist, []string{"192.168.1.20:5432", "nas.lan"})
	for _, c := range []struct {
		name  string
		w     *config.Workload
		names []string
		addr  string
		port  uint16
		sni   string
		want  Reason
	}{
		{"full: public", full, nil, "1.1.1.1", 443, "", ReasonInternetFull},
		{"full: not the LAN", full, nil, "192.168.1.1", 80, "", ReasonLANNone},
		{"full: not another qube", full, nil, "10.137.0.28", 7000, "", ReasonForbiddenAddress},
		{"full: not the tailnet here", full, nil, "100.64.0.1", 443, "", ReasonForbiddenAddress},
		{"name", list, []string{"pypi.org"}, "151.101.0.223", 443, "pypi.org", ReasonAllowlisted},
		{"name, other server name", list, []string{"pypi.org"}, "151.101.0.223", 443, "evil.com", ReasonSNINotAllowed},
		{"wildcard with port", list, []string{"cdn.hf.co"}, "1.2.3.4", 443, "", ReasonAllowlisted},
		{"wildcard, wrong port", list, []string{"cdn.hf.co"}, "1.2.3.4", 80, "", ReasonNotAllowlisted},
		{"range with port", list, nil, "93.184.215.14", 80, "", ReasonAllowlisted},
		{"range, wrong port", list, nil, "93.184.215.14", 443, "", ReasonUnresolvedIP},
		{"raw public ip", list, nil, "8.8.8.8", 53, "", ReasonUnresolvedIP},
		{"allowed name resolving to the LAN", list, []string{"pypi.org"}, "192.168.1.1", 443, "", ReasonNotAllowlisted},
		{"LAN address and port", list, nil, "192.168.1.20", 5432, "", ReasonAllowlisted},
		{"LAN address, other port", list, nil, "192.168.1.20", 22, "", ReasonUnresolvedIP},
		{"LAN by name", list, []string{"nas.lan"}, "192.168.1.30", 445, "", ReasonAllowlisted},
		{"LAN name, public address", list, []string{"nas.lan"}, "5.6.7.8", 445, "", ReasonNotAllowlisted},
		{"none", wl("", nil, "", nil), []string{"pypi.org"}, "151.101.0.223", 443, "", ReasonInternetNone},
	} {
		if got, _ := Permit(c.w, c.names, a(c.addr), c.port, "tcp", c.sni); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestLANNeverThroughTheExitNode(t *testing.T) {
	w := wl(config.InternetFull, nil, config.InternetFull, nil)
	w.Egress = "vpn"
	if p := PathFor(w, netip.MustParseAddr("192.168.1.1")); p != PathInternet {
		t.Errorf("LAN via %s", p)
	}
	if p := PathFor(w, netip.MustParseAddr("1.1.1.1")); p != PathExit {
		t.Errorf("internet via %s", p)
	}
}

func TestParseAllow(t *testing.T) {
	for _, s := range []string{"pypi.org", "*.hf.co:443", "1.2.3.4", "10.0.0.0/8:5432", "[fd00::1]:80", "fd00::/8", "[fd00::/8]:22",
		"pypi.org:443/udp", "8.8.8.8:53/udp", "10.0.0.0/8/tcp", "[fd00::1]:53/udp", "pypi.org/udp"} {
		if _, err := config.ParseAllow(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{"", "pypi.org:0", "pypi.org:99999", "*.*.x", "a b", "[fd00::1", "1.2.3.4:x", "pypi.org:443/sctp"} {
		if _, err := config.ParseAllow(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestTailnetPermit(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_6S2F, "tailnet-sni-names-device")
	a := netip.MustParseAddr
	list := []string{"notes", "chat:443", "100.100.0.0/16:22"}
	cp := []string{"notes", "notes.tail1.ts.net"}
	for _, c := range []struct {
		name  string
		names []string // asked or resolved
		peer  []string // the names of the device behind the address
		addr  string
		port  uint16
		sni   string
		want  Reason
	}{
		{"short name", []string{"notes"}, nil, "", 443, "", ReasonAllowlisted},
		{"full name", []string{"notes.tail1.ts.net"}, nil, "", 8443, "", ReasonAllowlisted},
		{"its address (the device's names)", nil, cp, "100.64.65.95", 443, "", ReasonAllowlisted},
		{"its address, its server name", nil, cp, "100.64.65.95", 443, "notes.tail1.ts.net", ReasonAllowlisted},
		{"its address, a listed but other device's server name", nil, cp, "100.64.65.95", 443, "chat.tail1.ts.net", ReasonSNINotAllowed},
		{"chat on its port", []string{"chat"}, nil, "", 443, "", ReasonAllowlisted},
		{"chat on another port", []string{"chat"}, nil, "", 8448, "", ReasonTailnetNotListed},
		{"a range with a port", []string{"x"}, nil, "100.100.1.2", 22, "", ReasonAllowlisted},
		{"a range, other port", []string{"x"}, nil, "100.100.1.2", 80, "", ReasonTailnetNotListed},
		{"another device", nil, []string{"peer", "peer.tail1.ts.net"}, "100.116.168.106", 7000, "", ReasonTailnetNotListed},
		{"a longer name is not a prefix match", []string{"notes2"}, nil, "", 443, "", ReasonTailnetNotListed},
	} {
		var addr netip.Addr
		if c.addr != "" {
			addr = a(c.addr)
		}
		if got := TailnetPermit(list, c.names, c.peer, addr, c.port, "tcp", c.sni); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestProtocols(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_2XS6, "udp-explicit", "port-narrows")
	a := netip.MustParseAddr
	w := wl(config.InternetAllowlist, []string{"pypi.org", "example.com:443/udp", "8.8.8.8:53/udp", "9.9.9.9", "files.org:443"}, "", nil)
	for _, c := range []struct {
		names []string
		addr  string
		port  uint16
		proto string
		want  Reason
		why   string
	}{
		{[]string{"pypi.org"}, "151.101.0.223", 443, "tcp", ReasonAllowlisted, "no protocol: TCP"},
		{[]string{"pypi.org"}, "151.101.0.223", 443, "udp", ReasonNotAllowlisted, "no protocol: not UDP"},
		{[]string{"example.com"}, "93.184.215.14", 443, "udp", ReasonAllowlisted, "/udp: UDP"},
		{[]string{"example.com"}, "93.184.215.14", 443, "tcp", ReasonNotAllowlisted, "/udp: not TCP"},
		{nil, "8.8.8.8", 53, "udp", ReasonAllowlisted, "address /udp: UDP"},
		{[]string{"files.org"}, "5.5.5.5", 443, "tcp", ReasonAllowlisted, "port: that port"},
		{[]string{"files.org"}, "5.5.5.5", 80, "tcp", ReasonNotAllowlisted, "port: not another"},
		{nil, "8.8.8.8", 53, "tcp", ReasonUnresolvedIP, "address /udp: not TCP"},
		{nil, "9.9.9.9", 853, "tcp", ReasonAllowlisted, "address, no protocol: TCP"},
		{nil, "9.9.9.9", 53, "udp", ReasonUnresolvedIP, "address, no protocol: not UDP"},
	} {
		if got, _ := Permit(w, c.names, a(c.addr), c.port, c.proto, ""); got != c.want {
			t.Errorf("%s: %s, want %s", c.why, got, c.want)
		}
	}
	// A DNS question names no protocol: an entry of either kind answers it.
	if ok, _ := MayAskName(w, "example.com", 0, ""); !ok {
		t.Error("example.com (UDP only) not answerable")
	}
}
