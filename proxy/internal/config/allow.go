package config

import (
	"net/netip"
	"strconv"
	"strings"
)

// AllowEntry is one allowlist entry: a host pattern ("pypi.org",
// "*.example.com") or an address or range, with an optional port.
type AllowEntry struct {
	Host   string
	Prefix netip.Prefix
	Port   uint16 // 0: any
	// Proto is "tcp" or "udp": the entry's "/udp" suffix, else tcp. UDP
	// must be asked for, since its server name (QUIC) is never checked.
	Proto string
}

// ParseAllow reads "host", "host:port", "*.host", "1.2.3.4", "10.0.0.0/8:5432",
// "[fd00::1]:80" or "fd00::/8". Errors name the entry.
func ParseAllow(s string) (AllowEntry, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	var e AllowEntry
	e.Proto = "tcp"
	for _, pr := range []string{"tcp", "udp"} {
		if rest, ok := strings.CutSuffix(s, "/"+pr); ok {
			s, e.Proto = rest, pr
		}
	}
	body := s
	if strings.HasPrefix(s, "[") { // [v6] or [v6]:port or [v6/len]:port
		i := strings.Index(s, "]")
		if i < 0 {
			return e, errEntry(s)
		}
		body = s[1:i]
		if rest := s[i+1:]; rest != "" {
			p, err := port(strings.TrimPrefix(rest, ":"))
			if err != nil || !strings.HasPrefix(rest, ":") {
				return e, errEntry(s)
			}
			e.Port = p
		}
	} else if strings.Count(s, ":") == 1 { // host:port, v4:port, v4/len:port
		h, ps, _ := strings.Cut(s, ":")
		p, err := port(ps)
		if err != nil {
			return e, errEntry(s)
		}
		body, e.Port = h, p
	}
	if pf, err := netip.ParsePrefix(body); err == nil {
		e.Prefix = pf.Masked()
		return e, nil
	}
	if a, err := netip.ParseAddr(body); err == nil {
		e.Prefix = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
		return e, nil
	}
	h, err := NormalizePattern(body)
	if err != nil {
		return e, errEntry(s)
	}
	e.Host = h
	return e, nil
}

func port(s string) (uint16, error) {
	p, err := strconv.ParseUint(s, 10, 16)
	if err != nil || p == 0 {
		return 0, errEntry(s)
	}
	return uint16(p), nil
}

type entryError string

func (e entryError) Error() string {
	return "allowlist entry " + strconv.Quote(string(e)) + ": want host, *.host, address or range, with an optional :port and /tcp or /udp"
}
func errEntry(s string) error { return entryError(s) }
