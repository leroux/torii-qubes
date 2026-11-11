// Package config is the proxy program's configuration: what dom0 derives
// from the machine file for one proxy qube and hands to it as a per-qube
// feature. In tests it comes from a file or a struct literal.
package config

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Link is how a workload reaches its proxy qube.
type Link string

const (
	// LinkNone: the workload is the proxy qube itself.
	LinkNone Link = "none"
	// LinkRouted: the proxy qube is the workload's netvm.
	LinkRouted Link = "routed"
	// LinkQrexec: the workload has no network card; each connection is a
	// qrexec call through dom0.
	LinkQrexec Link = "qrexec"
)

// Internet is what a workload may reach outside the tailnet.
type Internet string

const (
	InternetNone      Internet = "none"
	InternetFull      Internet = "full"
	InternetAllowlist Internet = "allowlist"
)

// Config is one proxy qube's configuration.
type Config struct {
	// Version is opaque; status reports it so dom0 can tell what is live.
	Version string `json:"version"`
	// ProxyQube is the name of the qube this program runs in.
	ProxyQube string `json:"proxy_qube"`
	// ControlURL is the coordination server: Tailscale, Headscale, or a
	// test server. Empty means Tailscale's default.
	ControlURL string `json:"control_url,omitempty"`
	// TailscaleLogs lets tsnet upload its client logs to Tailscale
	// (log.tailscale.com), over the proxy qube's own network. Off unless
	// set: see tailnet.SetTelemetry.
	TailscaleLogs bool `json:"tailscale_logs,omitempty"`
	// StateDir holds one folder per identity, on the private volume.
	StateDir string `json:"state_dir"`
	// LogDir holds one connection log per workload; empty keeps the log
	// in memory only.
	LogDir string `json:"log_dir,omitempty"`
	// ExitDNS is the resolver that internet names of workloads behind an
	// exit node are looked up at, reached through the exit node.
	ExitDNS string `json:"exit_dns,omitempty"`

	Listen Listen `json:"listen"`

	Workloads map[string]*Workload `json:"workloads"`

	// Egresses are the ways out through exit nodes that workloads here
	// use: one shared, internet-only identity each.
	Egresses map[string]*Egress `json:"egresses,omitempty"`
	// Unlisted decides sources on this proxy's interfaces that no routed
	// workload has; nil refuses them.
	Unlisted *Unlisted `json:"unlisted,omitempty"`
}

// Egress is a way out to the internet through tailnet exit nodes, tried
// in order. Its identity carries internet traffic only, never tailnet
// destinations. With no healthy exit node, its connections are refused.
type Egress struct {
	Hostname string   `json:"hostname"`
	Tags     []string `json:"tags"`
	// Exits are exit nodes (hostname, FQDN or address), most preferred first.
	Exits []string `json:"exits"`
	// FailbackAfter is how long, in seconds, a node ahead of the active
	// one must stay healthy before it is used again; default 120.
	FailbackAfter int `json:"failback_after,omitempty"`
	// Probe, when set, is a host:port dialled through the active exit node
	// as an end-to-end check.
	Probe       string `json:"probe,omitempty"`
	AuthKeyFile string `json:"auth_key_file,omitempty"`
}

// Unlisted is what sources no workload names get: internet access,
// optionally through an egress. Never a tailnet identity.
type Unlisted struct {
	Internet      Internet `json:"internet"`
	InternetAllow []string `json:"internet_allow,omitempty"`
	Egress        string   `json:"egress,omitempty"`
}

// UnlistedPrefix names an unlisted source's workload: "unlisted:<address>".
// Qube names cannot contain ':', so these never collide with one.
const UnlistedPrefix = "unlisted:"

// EgressIdentityKey is the key of an egress's identity among the
// identities: "egress:<name>".
func EgressIdentityKey(egress string) string { return "egress:" + egress }

// DefaultFailbackAfter is Egress.FailbackAfter's default, in seconds.
const DefaultFailbackAfter = 120

// Listen says where the program listens inside the proxy qube.
type Listen struct {
	// Intercept is the TCP address that redirected connections of routed
	// workloads arrive at.
	Intercept string `json:"intercept,omitempty"`
	// DNS is the UDP and TCP address that redirected DNS of routed
	// workloads arrives at.
	DNS string `json:"dns,omitempty"`
	// UDP is the address other UDP of routed workloads arrives at,
	// through TPROXY.
	UDP string `json:"udp,omitempty"`
	// LocalSOCKS is the SOCKS port for the proxy qube's own programs
	// (the link: none workload). Keep it on loopback.
	LocalSOCKS string `json:"local_socks,omitempty"`
	// Connect is the unix socket qrexec calls from workloads arrive at.
	Connect string `json:"connect,omitempty"`
	// Admin is the unix socket status, explain and the log are read from.
	Admin string `json:"admin,omitempty"`
}

// Workload is one qube whose traffic this proxy qube carries.
type Workload struct {
	Link Link `json:"link"`
	// Qube is the qube a qrexec workload's inbound connections go to,
	// when it is not the workload's name: one qube with several
	// identities is several workloads ("infra.chat") with one Qube.
	// Such a workload has inbound only; outbound calls are named by the
	// calling qube, which is the workload of that name.
	Qube string `json:"qube,omitempty"`
	// Address is the workload's Qubes IP, for routed workloads: the
	// source address that names it.
	Address string `json:"address,omitempty"`
	// Identity is the workload's tailnet identity; nil means none.
	Identity *Identity `json:"identity,omitempty"`
	// Inbound maps ports on the identity to ports in the workload.
	Inbound []Inbound `json:"inbound,omitempty"`
	// TailnetOutbound: the workload may open connections to the tailnet.
	// Where they may go is decided by grants only.
	TailnetOutbound bool `json:"tailnet_outbound,omitempty"`
	// TailnetAllow, when set, narrows TailnetOutbound to these devices: a
	// name (the device's short or full MagicDNS name, standing for all of
	// its addresses), an address or a range, each with an optional :port.
	// Grants still decide on top.
	TailnetAllow []string `json:"tailnet_allow,omitempty"`

	// Internet and LAN are the workload's access to two zones: the public
	// internet, and the private ranges of this machine's network. Each is
	// none, full or allowlist; an allowlist holds names ("pypi.org",
	// "*.example.com"), addresses or ranges, each with an optional :port.
	Internet      Internet `json:"internet,omitempty"`
	InternetAllow []string `json:"internet_allow,omitempty"`
	LAN           Internet `json:"lan,omitempty"`
	LANAllow      []string `json:"lan_allow,omitempty"`
	// Egress, when set, names the egress that carries the workload's
	// internet traffic (on the egress's identity, not the workload's own).
	Egress string `json:"egress,omitempty"`

	// Forwards are fixed outbound destinations: for qrexec workloads,
	// called as torii.Connect+f.<name>; for the proxy qube itself
	// (link: none), a loopback port each.
	Forwards []Forward `json:"forwards,omitempty"`
	// SOCKS lets a qrexec workload call torii.Connect+socks, or the
	// proxy qube's own programs use Listen.LocalSOCKS.
	SOCKS bool `json:"socks,omitempty"`
	// HTTP lets a qrexec workload call torii.Connect+http: an HTTP proxy
	// (CONNECT, and absolute-form requests for plain http://).
	HTTP bool `json:"http,omitempty"`
}

// Identity is a tailnet identity held for a workload.
type Identity struct {
	Hostname string `json:"hostname"`
	// Tags are advertised; empty with Login means a person's identity.
	Tags []string `json:"tags,omitempty"`
	// Login marks an identity that signs in as its person.
	Login bool `json:"login,omitempty"`
	// AuthKeyFile is the fallback: a one-time pre-approved key pasted
	// into the proxy qube. Empty, or no such file, means sign in by link.
	AuthKeyFile string `json:"auth_key_file,omitempty"`
	Ephemeral   bool   `json:"ephemeral,omitempty"`
}

// Inbound is one reverse forward.
type Inbound struct {
	Port   uint16 `json:"port"`
	Target uint16 `json:"target,omitempty"` // defaults to Port
	// TLS terminates HTTPS on the identity with its own certificate and
	// carries plain bytes to Target, as tailscale serve does.
	TLS bool `json:"tls,omitempty"`
}

// TargetPort is the port in the workload.
func (i Inbound) TargetPort() uint16 {
	if i.Target == 0 {
		return i.Port
	}
	return i.Target
}

// Forward is a fixed outbound destination.
type Forward struct {
	Name string `json:"name"`
	To   string `json:"to"` // host:port
	// Listen is the loopback port of a forward of the proxy qube itself
	// (link: none); qrexec forwards have none.
	Listen uint16 `json:"listen,omitempty"`
}

// Defaults for Listen.
const (
	DefaultIntercept  = "0.0.0.0:15001"
	DefaultDNS        = "0.0.0.0:15053"
	DefaultUDP        = "0.0.0.0:15002"
	DefaultLocalSOCKS = "127.0.0.1:1080"
	DefaultConnect    = "/run/torii/connect.sock"
	DefaultAdmin      = "/run/torii/admin.sock"
	DefaultExitDNS    = "1.1.1.1:53"
)

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and validates a config. Unknown fields are refused: a
// typo must not silently widen or narrow access.
func Parse(b []byte) (*Config, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.Normalize(); err != nil {
		return nil, err
	}
	return &c, nil
}

var (
	qubeName   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,30}$`)
	forwardArg = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	tagName    = regexp.MustCompile(`^tag:[a-z0-9][a-z0-9-]*$`)
)

// Normalize fills defaults and checks the config is coherent. It is
// called by Parse; tests that build a Config literal call it themselves.
func (c *Config) Normalize() error {
	if c.ProxyQube == "" {
		return fmt.Errorf("config: proxy_qube is required")
	}
	if c.StateDir == "" {
		return fmt.Errorf("config: state_dir is required")
	}
	if c.Listen.Intercept == "" {
		c.Listen.Intercept = DefaultIntercept
	}
	if c.Listen.DNS == "" {
		c.Listen.DNS = DefaultDNS
	}
	if c.Listen.UDP == "" {
		c.Listen.UDP = DefaultUDP
	}
	if c.Listen.LocalSOCKS == "" {
		c.Listen.LocalSOCKS = DefaultLocalSOCKS
	}
	if c.Listen.Connect == "" {
		c.Listen.Connect = DefaultConnect
	}
	if c.Listen.Admin == "" {
		c.Listen.Admin = DefaultAdmin
	}
	if c.ExitDNS == "" {
		c.ExitDNS = DefaultExitDNS
	}
	if _, err := netip.ParseAddrPort(c.ExitDNS); err != nil {
		return fmt.Errorf("config: exit_dns must be ip:port: %w", err)
	}
	addrs := map[netip.Addr]string{}
	hostnames := map[string]string{}
	for _, name := range sortedKeys(c.Egresses) {
		g := c.Egresses[name]
		if g == nil || !qubeName.MatchString(name) {
			return fmt.Errorf("config: egress %q", name)
		}
		if g.Hostname == "" || len(g.Tags) == 0 || len(g.Exits) == 0 {
			return fmt.Errorf("config: egress %s needs a hostname, tags and exits", name)
		}
		for _, t := range g.Tags {
			if !tagName.MatchString(t) {
				return fmt.Errorf("config: egress %s: bad tag %q", name, t)
			}
		}
		if g.FailbackAfter == 0 {
			g.FailbackAfter = DefaultFailbackAfter
		}
		if g.FailbackAfter < 0 {
			return fmt.Errorf("config: egress %s: failback_after %d", name, g.FailbackAfter)
		}
		if g.Probe != "" {
			if h, p, err := SplitHostPort(g.Probe); err != nil || h == "" || p == 0 {
				return fmt.Errorf("config: egress %s: probe must be host:port", name)
			}
		}
		h := strings.ToLower(g.Hostname)
		if other, ok := hostnames[h]; ok {
			return fmt.Errorf("config: %s and egress %s share hostname %s", other, name, h)
		}
		hostnames[h] = "egress " + name
	}
	if u := c.Unlisted; u != nil {
		w := &Workload{Link: LinkRouted, Address: "0.0.0.0", Internet: u.Internet, InternetAllow: u.InternetAllow, Egress: u.Egress}
		if err := w.normalize("unlisted"); err != nil {
			return err
		}
		if w.Internet == InternetNone {
			return fmt.Errorf("config: unlisted needs internet (leave unlisted out to refuse)")
		}
		u.Internet, u.InternetAllow = w.Internet, w.InternetAllow
		if u.Egress != "" && c.Egresses[u.Egress] == nil {
			return fmt.Errorf("config: unlisted: no egress %q", u.Egress)
		}
	}
	for _, name := range c.WorkloadNames() {
		w := c.Workloads[name]
		if w == nil {
			return fmt.Errorf("config: workload %q is empty", name)
		}
		if !qubeName.MatchString(name) {
			return fmt.Errorf("config: %q is not a qube name", name)
		}
		if err := w.normalize(name); err != nil {
			return err
		}
		if w.Link == LinkNone && name != c.ProxyQube {
			return fmt.Errorf("config: workload %s: link none is the proxy qube itself, %s", name, c.ProxyQube)
		}
		if w.Link == LinkRouted {
			a := netip.MustParseAddr(w.Address)
			if other, ok := addrs[a]; ok {
				return fmt.Errorf("config: %s and %s share address %s", other, name, a)
			}
			addrs[a] = name
		}
		if w.Egress != "" && c.Egresses[w.Egress] == nil {
			return fmt.Errorf("config: workload %s: no egress %q", name, w.Egress)
		}
		if w.Identity != nil {
			h := strings.ToLower(w.Identity.Hostname)
			if other, ok := hostnames[h]; ok {
				return fmt.Errorf("config: %s and %s share hostname %s", other, name, h)
			}
			hostnames[h] = name
		}
	}
	return nil
}

func (w *Workload) normalize(name string) error {
	errf := func(f string, a ...any) error {
		return fmt.Errorf("config: workload %s: %s", name, fmt.Sprintf(f, a...))
	}
	if w.Qube != "" {
		if w.Link != LinkQrexec || !qubeName.MatchString(w.Qube) {
			return errf("qube is for qrexec workloads, and must be a qube name")
		}
		if w.TailnetOutbound || w.Internet != "" && w.Internet != InternetNone || w.SOCKS || w.HTTP || len(w.Forwards) > 0 {
			return errf("a workload with qube %s has inbound only", w.Qube)
		}
	}
	switch w.Link {
	case LinkNone, LinkQrexec:
		if w.Address != "" {
			return errf("address is only for routed workloads")
		}
	case LinkRouted:
		a, err := netip.ParseAddr(w.Address)
		if err != nil {
			return errf("routed needs an address: %v", err)
		}
		w.Address = a.Unmap().String()
	default:
		return errf("link must be none, routed or qrexec, not %q", w.Link)
	}
	for _, z := range []struct {
		name  string
		mode  *Internet
		allow []string
	}{{"internet", &w.Internet, w.InternetAllow}, {"lan", &w.LAN, w.LANAllow}} {
		if *z.mode == "" {
			*z.mode = InternetNone
		}
		switch *z.mode {
		case InternetNone, InternetFull:
			if len(z.allow) > 0 {
				return errf("%s_allow needs %s: allowlist", z.name, z.name)
			}
		case InternetAllowlist:
			if len(z.allow) == 0 {
				return errf("%s: allowlist needs %s_allow", z.name, z.name)
			}
			for i, s := range z.allow {
				if _, err := ParseAllow(s); err != nil {
					return errf("%v", err)
				}
				z.allow[i] = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
			}
		default:
			return errf("%s must be none, full or allowlist, not %q", z.name, *z.mode)
		}
	}
	if w.Identity != nil {
		id := w.Identity
		if id.Hostname == "" {
			id.Hostname = name
		}
		if id.Login && len(id.Tags) > 0 {
			return errf("a login identity carries no tags")
		}
		if !id.Login && len(id.Tags) == 0 {
			return errf("an identity is tagged or a login")
		}
		for _, t := range id.Tags {
			if !tagName.MatchString(t) {
				return errf("bad tag %q", t)
			}
		}
	}
	if len(w.TailnetAllow) > 0 && !w.TailnetOutbound {
		return errf("tailnet_allow needs tailnet_outbound")
	}
	for i, s := range w.TailnetAllow {
		if _, err := ParseAllow(s); err != nil {
			return errf("%v", err)
		}
		w.TailnetAllow[i] = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	}
	if w.Identity == nil {
		if len(w.Inbound) > 0 {
			return errf("inbound needs an identity")
		}
		if w.TailnetOutbound {
			return errf("tailnet_outbound needs an identity")
		}
	}
	if w.Egress != "" && w.Internet == InternetNone {
		return errf("egress without internet")
	}
	seen := map[uint16]bool{}
	for _, in := range w.Inbound {
		if in.Port == 0 {
			return errf("inbound port 0")
		}
		if seen[in.Port] {
			return errf("inbound port %d twice", in.Port)
		}
		seen[in.Port] = true
	}
	if w.Link == LinkRouted && (len(w.Forwards) > 0 || w.SOCKS || w.HTTP) {
		return errf("forwards, socks and http are for qrexec workloads and the proxy qube")
	}
	ports := map[uint16]bool{}
	fw := map[string]bool{}
	for _, f := range w.Forwards {
		if !forwardArg.MatchString(f.Name) {
			return errf("forward name %q", f.Name)
		}
		if fw[f.Name] {
			return errf("forward %s twice", f.Name)
		}
		fw[f.Name] = true
		host, port, err := SplitHostPort(f.To)
		if err != nil || host == "" || port == 0 {
			return errf("forward %s: to must be host:port", f.Name)
		}
		switch {
		case w.Link == LinkNone && f.Listen == 0:
			return errf("forward %s needs a listen port", f.Name)
		case w.Link != LinkNone && f.Listen != 0:
			return errf("forward %s: listen is for the proxy qube's own forwards", f.Name)
		case f.Listen != 0 && ports[f.Listen]:
			return errf("listen port %d twice", f.Listen)
		}
		ports[f.Listen] = true
	}
	return nil
}

// WorkloadNames returns the workload names in order.
func (c *Config) WorkloadNames() []string {
	names := make([]string, 0, len(c.Workloads))
	for n := range c.Workloads {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ByAddress returns the routed workload whose Qubes address is a; with
// Unlisted set, an address no workload has is the unlisted workload
// "unlisted:<a>".
func (c *Config) ByAddress(a netip.Addr) (string, *Workload, bool) {
	a = a.Unmap()
	for _, n := range c.WorkloadNames() {
		w := c.Workloads[n]
		if w.Link == LinkRouted && w.Address == a.String() {
			return n, w, true
		}
	}
	if c.Unlisted != nil && a.IsValid() {
		n := UnlistedPrefix + a.String()
		w, _ := c.Lookup(n)
		return n, w, true
	}
	return "", nil, false
}

// Lookup returns the workload of a name: a listed one, or, with Unlisted
// set, an unlisted source ("unlisted:<address>"), routed, with the
// unlisted access and nothing else.
func (c *Config) Lookup(name string) (*Workload, bool) {
	if w, ok := c.Workloads[name]; ok {
		return w, true
	}
	rest, ok := strings.CutPrefix(name, UnlistedPrefix)
	if !ok || c.Unlisted == nil {
		return nil, false
	}
	a, err := netip.ParseAddr(rest)
	if err != nil {
		return nil, false
	}
	u := c.Unlisted
	return &Workload{Link: LinkRouted, Address: a.Unmap().String(), Internet: u.Internet,
		InternetAllow: u.InternetAllow, Egress: u.Egress}, true
}

// EgressNames returns the egress names in order.
func (c *Config) EgressNames() []string { return sortedKeys(c.Egresses) }

// EgressOf is the egress carrying w's internet traffic, or nil.
func (c *Config) EgressOf(w *Workload) *Egress {
	if w == nil || w.Egress == "" {
		return nil
	}
	return c.Egresses[w.Egress]
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NormalizePattern lowercases an allowlist entry and checks its form:
// "example.com" or "*.example.com".
func NormalizePattern(p string) (string, error) {
	p = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(p)), ".")
	rest := strings.TrimPrefix(p, "*.")
	if rest == "" || strings.Contains(rest, "*") || !validHostname(rest) {
		return "", fmt.Errorf("allowlist entry %q is not a hostname or *.hostname", p)
	}
	return p, nil
}

func validHostname(h string) bool {
	if len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}

// SplitHostPort is net.SplitHostPort with a numeric port.
func SplitHostPort(hp string) (string, uint16, error) {
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("missing port in %q", hp)
	}
	host, ps := hp[:i], hp[i+1:]
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	p, err := strconv.ParseUint(ps, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("bad port in %q", hp)
	}
	return host, uint16(p), nil
}
