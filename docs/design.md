# Design

Why torii is built the way it is. For using it, start with the
[README](../README.md); for what it is made of,
[`architecture.md`](architecture.md). The decisions below are each
recorded in `claims/`, and each section names the entries it led to; a
choice no entry rules is how the code does it today.

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
  nothing the second time ([STK-261126-B4CP](../claims/needs.md#stk-261126-b4cp-apply-changes-only-what-torii-owns-and-exactly-the-steps-plan-showed)).
- **See and check.** Show what each qube can reach, read from the live
  system and not from the file, and report where the two disagree
  ([STK-261126-4Q75](../claims/needs.md#stk-261126-4q75-show-reads-each-qubes-reach-from-the-live-machine-check-names-every-difference-from-the-file)).
- **Keep Qubes' isolation.** An offline qube stays without a network card.
  dom0 never handles traffic, and the code in dom0 stays small
  ([STK-261126-SM33](../claims/needs.md#stk-261126-sm33-torii-adds-no-qrexec-call-network-path-or-dom0-exposure-beyond-those-the-file-declares)).
- **No standing credentials.** Identities join by sign-in links approved by a
  tag owner. Grants are printed as proposals for a person to review, never
  pushed ([STK-261126-KHZB](../claims/needs.md#stk-261126-khzb-each-tailnet-identity-is-one-qubes-joins-only-by-a-persons-approval-and-dom0-holds-no-tailnet-credential)).
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
on the tailnet a qube may go. A port must be allowed by both
([STK-261126-KZK8](../claims/needs.md#stk-261126-kzk8-a-carried-qubes-connection-is-relayed-only-if-its-entry-allows-it-every-other-connection-is-refused)). Where each part runs is in
[`architecture.md`](architecture.md).

## Decisions

### One shared proxy qube, with one identity per workload

A proxy qube, `sys-torii` by default, holds the identities of every workload
on the machine and carries their traffic. This is the shape of Istio's
ambient mode: one connection-level proxy per node holding that node's
workload identities. Workloads that need stronger separation get a dedicated
proxy qube (`proxies:`, `proxy: <name>`), much as Istio adds
sidecars only where they are needed. Each identity is a device of its own
([SW-261127-WNGN](../claims/identity.md#sw-261127-wngn-each-identity-is-a-tailnet-device-of-its-own-with-its-own-name-and-state)); what a shared proxy qube holds if compromised is
stated in [STK-261126-SM33](../claims/needs.md#stk-261126-sm33-torii-adds-no-qrexec-call-network-path-or-dom0-exposure-beyond-those-the-file-declares).

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
workloads can share a proxy cleanly. Blocking is structural because
forwarding is off and a guard keeps it off ([SW-261127-B6Z6](../claims/links.md#sw-261127-b6z6-a-routed-qubes-traffic-reaches-the-program-and-nothing-else-interception-with-forwarding-off),
[SW-261127-JWK2](../claims/links.md#sw-261127-jwk2-a-guard-loaded-at-boot-keeps-the-proxy-qube-forwarding-nothing-whatever-the-program-does)). Throughput is in
[`performance.md`](performance.md). Anyone may still run `tailscaled` in a
qube of their own. Leave that qube out of the file, or give it
`network: default` or a netvm of its own, and the proxy doesn't carry it.

### Offline workloads: qrexec calls in the direction of the connection

Every qrexec call goes the same way as the connection it carries. For
inbound, the proxy qube calls `qubes.ConnectTCP` into the workload, on listed
ports only. For outbound, the workload calls `torii.Connect` in the proxy
qube. dom0's policy allows each call by name and port, and names the caller,
so the proxy knows which workload is asking ([SW-261127-7C3Y](../claims/links.md#sw-261127-7c3y-a-qrexec-caller-is-the-qube-dom0-names-never-one-the-caller-states)).

<!-- charter:embed SW-261127-M36P -->
> **[SW-261127-M36P](../claims/links.md#sw-261127-m36p-every-qrexec-call-runs-in-the-direction-of-the-connection-it-carries-and-only-the-declared-ones-are-allowed) Every qrexec call runs in the direction of the connection it carries, and only the declared ones are allowed**
>
> Outbound, a carried qube with no netvm calls `torii.Connect` in its own
> proxy, with `+socks`, `+http` or `+f.<forward>`, and only those its entry
> declares. Inbound, the proxy calls `qubes.ConnectTCP` into a carried qube,
> routed or not, only on the target ports its entry or a service lists.
> Between qubes on this machine, `qubes.ConnectTCP` is allowed only from the
> qubes `local-inbound` names, on its port. A qube that declares no
> outbound access is allowed no `torii.Connect` call, and calls
> `qubes.ConnectTCP` only where another qube's `local-inbound` names it.
> torii's deny file, read after every mesh's own, denies `torii.Connect`
> and `torii.Status` to every caller the allows do not name;
> `qubes.ConnectTCP` beyond them is left to Qubes' own policy (observation
> QubesConnectTCPDenied). A hand-written policy file read before torii's
> can widen any of these, and check names it (STK-261126-4Q75).
<!-- /charter:embed -->

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
`vif*` drop forged sources ([SW-261127-BHHX](../claims/links.md#sw-261127-bhhx-a-routed-qube-is-named-by-its-qubes-source-address-which-qubes-keeps-it-from-forging), resting on the
observation [QubesAntispoof](../claims/observations.md#qubesantispoof)).
IPv6 is turned off behind the proxy ([SW-261127-3Q23](../claims/apply.md#sw-261127-3q23-every-proxy-qube-has-ipv6-off-for-itself-and-every-qube-behind-it)). The guard is
[SW-261127-JWK2](../claims/links.md#sw-261127-jwk2-a-guard-loaded-at-boot-keeps-the-proxy-qube-forwarding-nothing-whatever-the-program-does), the interception [SW-261127-B6Z6](../claims/links.md#sw-261127-b6z6-a-routed-qubes-traffic-reaches-the-program-and-nothing-else-interception-with-forwarding-off).

### Allowlists by name, not address

IP rules can't follow names whose addresses change or are shared by a CDN.
So the program answers the workload's DNS, remembers which names each
workload resolved, and decides each connection by name: the name asked for
(SOCKS, HTTP proxy, forward) or resolved ([SW-261127-2R9T](../claims/connections.md#sw-261127-2r9t-a-name-entry-counts-only-for-a-name-the-qube-asked-for-or-resolved-through-the-proxy-to-that-address),
[SW-261127-M7G9](../claims/connections.md#sw-261127-m7g9-a-name-resolves-for-the-qube-and-for-the-proxy-only-to-addresses-the-qube-may-reach-under-it)), plus the TLS server name when one is sent, read from
the ClientHello without decrypting anything ([SW-261127-6S2F](../claims/connections.md#sw-261127-6s2f-under-an-allowlist-a-tls-server-name-must-be-allowed-too-and-nothing-is-relayed-before-it-is-read)). A raw
address that nobody resolved is refused. This stops domain fronting with an
allowed name, though not raw traffic to another site on a shared address.
The remaining gaps are listed under [Limits](#limits).

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
dialled only on the workload's identity), and `torii grants` proposes only
`autogroup:internet` for its tag ([SW-261127-WSWG](../claims/identity.md#sw-261127-wswg-an-egresss-identity-carries-only-the-internet-and-grants-propose-it-nothing-else)). Sharing a *path* is harmless, since everyone
behind one exit node shares its address anyway. Sharing tailnet
*authorisation* is how compartments merge, so egresses don't do it.

Rules attach to the qube, not to the identity or the egress
([SW-261127-PXX9](../claims/connections.md#sw-261127-pxx9-a-connection-is-decided-against-the-entry-of-the-qube-it-came-from-by-the-way-it-arrived)). That is what leaves room for per-qube firewalling
(prompts, learned rules) later without changing either.

### Exit nodes in order, failing closed

An egress lists exit nodes in order. The proxy uses the first healthy one,
fails over when it goes down, and fails back to a preferred one once that
has stayed healthy for a hold-down (`failback-after`, 2 minutes), so a
flapping home exit node doesn't move the qubes' address every minute
([SW-261127-BNFC](../claims/egress.md#sw-261127-bnfc-exit-nodes-are-used-in-the-files-order-failing-over-at-once-and-back-only-after-a-hold-down)).

Healthy means: listed online and offering an exit node in the tailnet map
(free, catches withdrawn routes and ACL changes), answering a Tailscale ping
(the map lags by tens of seconds), and real connections through it not all
failing (three or more destinations failing within 30 s with none succeeding
trips it: one dead site doesn't). No third-party probe runs by default; it
would send a recognisable connection out of every exit node. `probe:` adds
one for those who want it ([SW-261127-8DFY](../claims/egress.md#sw-261127-8dfy-an-exit-node-is-healthy-while-listed-answering-pings-and-not-failing-every-real-connection-through-it)).

Failing closed holds in two independent places. The program dials straight
into the egress identity's userspace network stack and never uses
`tsnet.Server.Dial`, which falls back to the system network for addresses
without a tailnet route, so without an exit node nothing has a route out
([SW-261127-K5TK](../claims/egress.md#sw-261127-k5tk-an-egresss-traffic-is-dialled-only-into-its-identitys-own-network-stack-never-the-system-network)). And the engine refuses exit-path connections, and
answers SERVFAIL to their DNS, while the egress has no healthy node
([SW-261127-HJNX](../claims/egress.md#sw-261127-hjnx-while-an-egress-has-no-healthy-exit-node-its-connections-are-refused-at-once-and-its-dns-fails)). A switch is one prefs change from
the old node to the new; the exit node is never cleared, because for a stock
Tailscale client clearing it is exactly the switch back to direct
([SW-261127-6YGC](../claims/egress.md#sw-261127-6ygc-an-egresss-exit-node-is-never-cleared-a-switch-is-one-change-from-the-old-node-to-the-new)). The whole flow is in
[`walkthrough-egress.md`](walkthrough-egress.md).

### Unlisted qubes and disposables

A qube attached to a proxy but not in the file is decided by that proxy's
`unlisted:`, `refuse` by default: attaching a qube by mistake gets it
nothing, and never a tailnet identity ([SW-261127-PXX9](../claims/connections.md#sw-261127-pxx9-a-connection-is-decided-against-the-entry-of-the-qube-it-came-from-by-the-way-it-arrived),
[SW-261127-BHHX](../claims/links.md#sw-261127-bhhx-a-routed-qube-is-named-by-its-qubes-source-address-which-qubes-keeps-it-from-forging)). A disposable can't be listed (its address changes per start), so
its disposable template's proxy's `unlisted:` is its policy; the documented
way is a proxy of their own, so the main proxy can stay `refuse`.

### Configuration in features, status over one read-only service

dom0 writes each proxy qube's config as Qubes features (`vm-config.torii*`),
which the qube can read through QubesDB but not change. QubesDB caps a value
under 3 KB, so the JSON is compressed and split, with a hash to detect
partial writes. A half-written or invalid config never replaces the running
one ([SW-261127-XGDY](../claims/apply.md#sw-261127-xgdy-a-proxy-takes-its-config-whole-or-not-at-all-and-a-failed-check-never-replaces-the-running-one)). Status and the decision log come back to dom0
through one qrexec service, `torii.Status`, which changes nothing
([SW-261127-B8BM](../claims/seeing.md#sw-261127-b8bm-the-status-service-is-read-only-it-reports-and-changes-nothing-in-the-proxy)).

### Packages travel inside the dom0 RPM

The dom0 package carries the proxy and client packages as `.deb` and
`.rpm`. On first run the tool clones a minimal template into its own, copies
the package in and installs it, so no template needs a repository for
torii ([SW-261127-HSHY](../claims/apply.md#sw-261127-hshy-the-proxy-package-comes-from-the-dom0-packages-bundle-with-no-repository-for-torii)). Each identity's Tailscale state lives in the
proxy qube's private volume ([SW-261127-WNGN](../claims/identity.md#sw-261127-wngn-each-identity-is-a-tailnet-device-of-its-own-with-its-own-name-and-state)). A rebuilt machine gets new identities and new sign-ins rather than
restored keys.

### A dom0 tool that plans from a snapshot

`plan` is a pure function of the file and a snapshot of the live machine,
and `apply` executes that plan step by step, stopping at the first failure
([SW-261127-63CS](../claims/apply.md#sw-261127-63cs-apply-makes-the-plans-steps-in-order-stops-at-the-first-that-fails-and-a-second-run-continues)); what it may touch is [SW-261127-G26T](../claims/apply.md#sw-261127-g26t-apply-changes-only-what-torii-owns-its-template-its-proxy-qubes-its-policy-files-and-named-workload-settings). The flow
is in [`walkthrough-apply.md`](walkthrough-apply.md).
That keeps the dom0 code small and reviewable, and lets plans be
golden-tested without a Qubes machine ([`testing.md`](testing.md)).

## What a compromise reaches

- **A workload** reaches only what its entry allows: its declared outbound
  zones, and on the tailnet only where grants let its identity go. Other
  qubes' addresses and the proxy qube's own sockets are never reachable
  through the proxy. An offline workload with no outbound access can make no
  calls at all ([STK-261126-KZK8](../claims/needs.md#stk-261126-kzk8-a-carried-qubes-connection-is-relayed-only-if-its-entry-allows-it-every-other-connection-is-refused)).
- **The proxy qube** holds every identity on it and sees the traffic it
  relays, as any netvm does, so treat it like one. From there it can call
  into workloads only on their declared inbound ports. torii's policy
  gives it no calls into dom0: dom0 reads its status, and hands it its config
  as features. Put sensitive workloads on a dedicated proxy to keep them
  apart ([STK-261126-SM33](../claims/needs.md#stk-261126-sm33-torii-adds-no-qrexec-call-network-path-or-dom0-exposure-beyond-those-the-file-declares)).
- **dom0** stores no Tailscale credential. Auth keys, when used, go only to
  the proxy qube ([SW-261127-3TPX](../claims/identity.md#sw-261127-3tpx-an-identity-signs-in-by-a-link-a-person-approves-or-a-one-time-key-that-only-the-proxy-qube-keeps)).

## Limits

What torii does not cover, as the needs state it ([STK-261126-KZK8](../claims/needs.md#stk-261126-kzk8-a-carried-qubes-connection-is-relayed-only-if-its-entry-allows-it-every-other-connection-is-refused),
[STK-261126-346B](../claims/needs.md#stk-261126-346b-a-qube-on-an-egress-reaches-the-internet-only-through-that-egresss-exit-nodes-or-not-at-all)), and how the code stands today.

- Other qubes' addresses (`10.137–10.139/16`) are never reachable through
  the proxy, whatever the file says; use `local-inbound`.
- A name entry without a port allows every TCP port of that name; write
  `name:443` to narrow it. UDP is never implied: add `name:443/udp` for QUIC,
  whose server name the proxy does not check (it decides UDP by the name
  the qube resolved).
- `outbound.internet: all` resolves any name, so such a qube can send data
  out over DNS; a `*.domain` entry lets it reach that domain's DNS servers.
- An allowlist is checked by name: the name asked for or resolved, and the
  TLS server name. Through the HTTP proxy, plain HTTP is checked per request
  too. Raw TCP or QUIC to an address that an allowed name shares (a CDN) can
  still reach another site there.
- No IPv6 behind the proxy: the tool turns Qubes' `ipv6` feature off on
  proxy qubes (their clients inherit it), and the proxy drops any IPv6 a
  workload sets up for itself. The proxy qube's own IPv6 is unaffected.
- Disposables can't be listed, since their address changes on each start.
  A routed disposable is an unlisted qube of its disposable template's proxy
  and gets that proxy's `unlisted:`; disposables with no network card that
  call the proxy over qrexec are not supported.
- A disposable's netvm is its disposable template's, not that of the qube
  that opened it. `check` warns when a qube on an egress has a
  `default_dispvm` whose netvm isn't a torii proxy; torii never sets
  `default_dispvm` itself.
- With an egress, LAN traffic (`outbound.lan`) still goes direct through the
  proxy's netvm; `plan` notes it. Connections open through an exit node break
  when the egress switches to another one: the public address changes.
- One tailnet per machine file (one coordination server).
- Not built yet: removing tailnet devices from `check`; a GUI; Tailscale
  Services; measurements of memory per identity and userspace throughput.

## Prior art

| | What it tells us |
| --- | --- |
| [Tailscale Kubernetes operator](https://tailscale.com/kb/1236/kubernetes-operator) | The closest model: declarations become tailnet ingress and egress proxies, shared or dedicated. Qubes is the cluster and dom0 the control plane |
| [TSDProxy](https://almeidapaulopt.github.io/tsdproxy/docs/v3/concepts/) | Docker labels become `tsnet` nodes, one per container or shared |
| [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) | Named inbound endpoints without a device each; a likely later backend for `services:` |
| Istio ambient mode | One connection-level proxy per node holding its workloads' identities, as `sys-torii` does for one machine |
| `qubes.ConnectTCP`, `qubes.UpdatesProxy` | Qubes' own precedents for reaching across qubes over qrexec, and for a proxy that offline qubes call |
