# Design

Why lintel is built the way it is. For using it, start with the
[README](../README.md).

## The problem

What a qube can reach over the network isn't one setting in Qubes. It is
the combined effect of several mechanisms, each configured somewhere else:

| Where | What it holds |
| --- | --- |
| `qvm-prefs` | the qube's netvm |
| `qvm-firewall` | what its netvm lets it reach |
| `/etc/qubes/policy.d/` in dom0 | which qrexec calls it may make or receive |
| files in qubes and templates | VPN daemons and their state, forwards, proxy settings |
| the tailnet's admin console | devices, keys, tags and grants |

Each piece is fine on its own. Combined, they make connectivity hard to set
up, hard to see, and hard to trust. Take one offline service reachable over
Tailscale. Done by hand, that is a gateway qube running `tailscaled` with
state kept across restarts, a `qvm-connect-tcp` forward, a dom0 policy line,
a tag and a grant. Afterwards no single place answers "what can this qube
reach, and what can reach it?" A qube meant to be offline that still has a
netvm stays invisible until something leaks.

Running Tailscale in one network qube is simpler, but it merges compartments.
Every qube behind it becomes one tailnet device with one set of grants.

## Goals

- **Declare.** One file per machine states each qube's network card,
  internet access, tailnet identity, inbound ports and outbound reach.
- **Apply.** Make the machine match the file. Running it twice changes
  nothing the second time.
- **See and check.** Show what each qube can reach, read from the live
  system and not from the file, and report where the two disagree.
- **Keep Qubes' isolation.** An offline qube stays without a network card.
  dom0 never handles traffic, and the code in dom0 stays small.
- **No standing credentials.** Identities join by sign-in links approved by a
  tag owner. Grants are printed as proposals for a person to review, never
  pushed.
- **Stock Qubes**, through supported interfaces only: the Admin API,
  features, policy files and services. No patches.

Out of scope: managing several machines from one place (each dom0 is in
charge of itself; [Salt](salt.md) can distribute the files), overlay networks
other than Tailscale-compatible ones, and a GUI (for now).

## The model

Every connection, in every pattern, is made of four parts:

1. **The proxy qube** holds the workload's tailnet identity and runs the
   proxy program.
2. **The link** between the workload and its proxy qube is either *qrexec*
   (the workload has no network card; each connection is a qrexec call
   through dom0) or *routed* (the proxy qube is the workload's netvm).
3. **Inbound** maps ports on the identity to ports in the workload.
4. **Outbound** says which destinations the workload may open
   connections to. They fall in three zones, decided by the address actually
   dialled: internet, LAN and tailnet.

Two layers of policy sit on top. The file says what a qube *offers* and what
it *may try*. The tailnet's grants decide who may use those ports and where
on the tailnet a qube may go. A port must be allowed by both.

## Decisions

### One shared proxy qube, with one identity per workload

A proxy qube, `sys-lintel` by default, holds the identities of every workload
on the machine and carries their traffic. This is the shape of Istio's
ambient mode: one connection-level proxy per node holding that node's
workload identities. Workloads that need stronger separation get a dedicated
proxy qube (`proxies:`, `proxy: <name>`), much as Istio adds
sidecars only where they are needed.

### Our own `tsnet` program rather than stock `tailscaled`

Both options would share the file and the dom0 tool. They differ inside the
proxy qube:

| | Stock `tailscaled` | Own program on `tsnet` (chosen) |
| --- | --- | --- |
| Identities | One daemon per workload, each with its own state, socket and unit | One process, one `tsnet` node per workload |
| Routed workloads sharing a proxy | Needs a kernel-mode instance per workload, routing by source, and Tailscale's and Qubes' firewall rules mixed in one qube | Connections are intercepted; the source address picks the identity |
| Blocking the internet | Firewall rules that must stay correct | Structural: forwarding is off, so nothing leaves that the program doesn't relay |
| A connection log for `show` | Pieced together from several daemons | One log per workload |
| Cost | Kernel-speed WireGuard; config generation only | Userspace WireGuard; a program on the security path |

The program is the simpler system to run, and the only one where routed
workloads can share a proxy cleanly. Throughput is in
[`performance.md`](performance.md). Anyone may still run `tailscaled` in a
qube of their own. Leave that qube out of the file, or give it
`network: default` or a netvm of its own, and the proxy doesn't carry it.

### Offline workloads: qrexec calls in the direction of the connection

Every qrexec call goes the same way as the connection it carries. For
inbound, the proxy qube calls `qubes.ConnectTCP` into the workload, on listed
ports only. For outbound, the workload calls `lintel.Connect` in the proxy
qube. dom0's policy allows each call by name and port, and names the caller,
so the proxy knows which workload is asking.

A workload that declares no outbound access makes no qrexec calls at all.
If it is compromised, it can only answer connections others opened. A
reverse tunnel (the workload holding a connection open to the proxy) was
rejected. It would give every workload a standing channel out, and put a
tunnel protocol inside the offline qube.

### Routed workloads: interception, with forwarding off

In the proxy qube, kernel forwarding is off, and a boot-time nftables guard
keeps it off whatever happens to the program: stopped, crashed or
reloading. nftables hands every TCP connection, all DNS and other UDP from
the workloads' interfaces to the program. The source address names the
workload, and the program can trust it because Qubes' anti-spoofing rules on
`vif*` drop forged sources. IPv6 is turned off behind the proxy.

### Allowlists by name, not address

IP rules can't follow names whose addresses change or are shared by a CDN.
So the program answers the workload's DNS, remembers which names each
workload resolved, and decides each connection by name: the name asked for
(SOCKS, HTTP proxy, forward) or resolved, plus the TLS server name when one
is sent, read from the ClientHello without decrypting anything. A raw
address that nobody resolved is refused. This stops domain fronting with an
allowed name, though not raw traffic to another site on a shared address.
The remaining gaps are listed in [`reference.md`](reference.md#limits).

### Identity, egress and policy are separate

Three questions, each answered in one place: *who is this qube on the
tailnet* (`tailnet:`, its identity), *which way does its internet go*
(`egress:`), and *what may it reach* (`outbound:`). An **egress** is a way
out through tailnet exit nodes, defined once and used by name. Each proxy
that carries it holds one shared identity for it, so a qube that only needs
its internet forced through an exit node gets no identity of its own. A qube
with both uses both: its tailnet traffic on its identity, its internet on the
egress's, chosen per connection by the zone of the address dialled.

An egress identity is internet-only, held on both sides: the proxy never
sends tailnet destinations through it (they are a zone of their own,
dialled only on the workload's identity), and `lintel grants` proposes only
`autogroup:internet` for its tag. Sharing a *path* is harmless, since everyone
behind one exit node shares its address anyway. Sharing tailnet
*authorisation* is how compartments merge, so egresses don't do it.

Rules attach to the qube, not to the identity or the egress. That is what
leaves room for per-qube firewalling (prompts, learned rules) later without
changing either.

### Exit nodes in order, failing closed

An egress lists exit nodes in order. The proxy uses the first healthy one,
fails over when it goes down, and fails back to a preferred one once that
has stayed healthy for a hold-down (`failback-after`, 2 minutes), so a
flapping home exit node doesn't move the qubes' address every minute.

Healthy means: listed online and offering an exit node in the tailnet map
(free, catches withdrawn routes and ACL changes), answering a Tailscale ping
(the map lags by tens of seconds), and real connections through it not all
failing (three or more destinations failing within 30 s with none succeeding
trips it: one dead site doesn't). No third-party probe runs by default; it
would send a recognisable connection out of every exit node. `probe:` adds
one for those who want it.

Failing closed holds in two independent places. The program dials straight
into the egress identity's userspace network stack and never uses
`tsnet.Server.Dial`, which falls back to the system network for addresses
without a tailnet route, so without an exit node nothing has a route out.
And the engine refuses exit-path connections, and answers SERVFAIL to their
DNS, while the egress has no healthy node. A switch is one prefs change from
the old node to the new; the exit node is never cleared, because for a stock
Tailscale client clearing it is exactly the switch back to direct.

### Unlisted qubes and disposables

A qube attached to a proxy but not in the file is decided by that proxy's
`unlisted:`, `refuse` by default: attaching a qube by mistake gets it
nothing. A disposable can't be listed (its address changes per start), so
its disposable template's proxy's `unlisted:` is its policy; the documented
way is a proxy of their own, so the main proxy can stay `refuse`.

### Configuration in features, status over one read-only service

dom0 writes each proxy qube's config as Qubes features (`vm-config.lintel*`),
which the qube can read through QubesDB but not change. QubesDB caps a value
under 3 KB, so the JSON is compressed and split, with a hash to detect
partial writes. A half-written or invalid config never replaces the running
one. Status and the decision log come back to dom0 through one qrexec
service, `lintel.Status`.

### Packages travel inside the dom0 RPM

The dom0 package carries the proxy and client packages as `.deb` and
`.rpm`. On first run the tool clones a minimal template into its own, copies
the package in and installs it, so no template needs a repository for
lintel. Each identity's Tailscale state lives in the proxy qube's private
volume. A rebuilt machine gets new identities and new sign-ins rather than
restored keys.

### A dom0 tool that plans from a snapshot

`plan` is a pure function of the file and a snapshot of the live machine,
and `apply` executes that plan step by step, stopping at the first failure.
That keeps the dom0 code small and reviewable, and lets plans be
golden-tested without a Qubes machine ([`testing.md`](testing.md)).

## What a compromise reaches

- **A workload** reaches only what its entry allows: its declared outbound
  zones, and on the tailnet only where grants let its identity go. Other
  qubes' addresses and the proxy qube's own sockets are never reachable
  through the proxy. An offline workload with no outbound access can make no
  calls at all.
- **The proxy qube** holds every identity on it and sees the traffic it
  relays, as any netvm does, so treat it like one. From there it can call
  into workloads only on their declared inbound ports. lintel's policy
  gives it no calls into dom0: dom0 reads its status, and hands it its config
  as features. Put sensitive workloads on a dedicated proxy to keep them
  apart.
- **dom0** stores no Tailscale credential. Auth keys, when used, go only to
  the proxy qube.

## Prior art

| | What it tells us |
| --- | --- |
| [Tailscale Kubernetes operator](https://tailscale.com/kb/1236/kubernetes-operator) | The closest model: declarations become tailnet ingress and egress proxies, shared or dedicated. Qubes is the cluster and dom0 the control plane |
| [TSDProxy](https://almeidapaulopt.github.io/tsdproxy/docs/v3/concepts/) | Docker labels become `tsnet` nodes, one per container or shared |
| [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) | Named inbound endpoints without a device each; a likely later backend for `services:` |
| Istio ambient mode | One connection-level proxy per node holding its workloads' identities, as `sys-lintel` does for one machine |
| `qubes.ConnectTCP`, `qubes.UpdatesProxy` | Qubes' own precedents for reaching across qubes over qrexec, and for a proxy that offline qubes call |
