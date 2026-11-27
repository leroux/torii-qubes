# Architecture

What torii is made of, where each part runs, and what crosses between
them. Why it is shaped so is in [`design.md`](design.md); how one
connection or one apply moves through these parts is in the walkthroughs
([a connection](walkthrough-connection.md), [plan and apply](walkthrough-apply.md),
[an egress](walkthrough-egress.md)). The decisions each part carries are in
`claims/`, and each section names them.

## The picture

```
 dom0 ─────────────────────────────────────────────────────────────────┐
 │ torii (Python): reads ~/torii.yaml, plans, applies through the      │
 │ Admin API; writes 50-torii.policy; reads status back                │
 └───────┬──────────────────────────────▲──────────────────────────────┘
         │ features vm-config.torii*    │ torii.Status (read-only)
         │ netvm, services, tags        │
 ┌───────▼──────────────────────────────┴───────┐
 │ sys-torii (proxy qube)                       │   netvm (sys-firewall,
 │  torii-guard: forwards nothing, from boot    │──▶ a VPN qube...) ──▶ internet, LAN
 │  torii-proxy: one tsnet node per identity,   │
 │  decides and relays every connection         │──▶ tailnet (each identity,
 └──▲───────────────────▲──────────────┬────────┘    and egress exit nodes)
    │ torii.Connect      │ vif* packets │ qubes.ConnectTCP (inbound,
    │ (qrexec via dom0)  │ (netvm)      │ listed ports only)
 ┌──┴──────────────┐ ┌──┴────────────┐ │
 │ network: none   │ │ network: proxy│◀┘
 │ torii-client on │ │ (routed)      │
 │ loopback        │ │               │
 └─────────────────┘ └───────────────┘
```

Four kinds of thing: the tool in dom0, the template and packages it
installs, the proxy qube, and the qubes it carries. dom0 configures; the
proxy qube decides and relays; dom0 never touches a connection.

## dom0: the `torii` tool

A Python package, `dom0/torii`, run as `torii` in dom0. Each command is a
function in [cli.py](../dom0/torii/cli.py), wired up by
[`cli.main`](../dom0/torii/cli.py#L297). Its parts, in the order a `plan` uses
them:

- **The file.** [`machine.parse`](../dom0/torii/machine.py#L551) reads
  `~/torii.yaml` into a `Machine`, whole or not at all: [SW-261127-9BNW](../claims/apply.md#sw-261127-9bnw-the-machine-file-is-read-whole-and-refused-whole-when-a-key-a-network-or-a-value-is-wrong).
- **The live machine.** [`live.take`](../dom0/torii/live.py#L83) reads a
  [`Snapshot`](../dom0/torii/live.py#L44) through the Admin API: every qube's netvm,
  class and tags, the features of the qubes the file names and of those
  carrying torii's tags (Qubes services among them), and every qrexec
  policy file.
- **What the file asks for.** [derive.py](../dom0/torii/derive.py)
  turns the `Machine` into what each part should hold:
  [`derive.proxy_config`](../dom0/torii/derive.py#L53) for a proxy,
  [`derive.qube_state`](../dom0/torii/derive.py#L192) for a carried qube,
  [`derive.policy`](../dom0/torii/derive.py#L216) for the policy file, and
  [`derive.grants`](../dom0/torii/derive.py#L261) for the tailnet.
- **The steps.** [`plan.make`](../dom0/torii/plan.py#L96) compares the two
  and lists the steps; [`Applier.run`](../dom0/torii/apply.py#L45) makes them.
  What it may touch is [SW-261127-G26T](../claims/apply.md#sw-261127-g26t-apply-changes-only-what-torii-owns-its-template-its-proxy-qubes-its-policy-files-and-named-workload-settings); the order and the stop at a
  failure, [SW-261127-63CS](../claims/apply.md#sw-261127-63cs-apply-makes-the-plans-steps-in-order-stops-at-the-first-that-fails-and-a-second-run-continues).
- **Reading back.** [`show.qube_view`](../dom0/torii/show.py#L97) and
  [`show.check_problems`](../dom0/torii/show.py#L365) read the snapshot and each
  proxy's status, which [`status.read`](../dom0/torii/status.py#L13) fetches
  over `torii.Status`.

The [plan and apply walkthrough](walkthrough-apply.md) follows these in
order. dom0's part in the traffic is nothing at all:

<!-- charter:embed STK-261126-SM33 -->
> **[STK-261126-SM33](../claims/needs.md#stk-261126-sm33-torii-adds-no-qrexec-call-network-path-or-dom0-exposure-beyond-those-the-file-declares) torii adds no qrexec call, network path or dom0 exposure beyond those the file declares**
>
> The policy torii writes allows only: a carried qube with outbound access
> to call `torii.Connect` in its own proxy; a proxy to call
> `qubes.ConnectTCP` into a qube on its listed inbound ports; and dom0 and
> the file's `status-readers` to call `torii.Status`. A proxy qube can call
> nothing in dom0, and a carried qube with no outbound access can call
> nothing. A qube with `network: none` has no netvm.
>
> A proxy qube's kernel forwards no packet, whether the proxy program runs,
> has stopped or is reloading; a config half delivered or invalid never
> replaces the running one. dom0 relays no traffic and parses nothing a
> qube sends but the status the owner reads. torii changes Qubes only
> through the Admin API, features, policy files and services.
>
> Accepted, and stated: a compromised proxy qube holds every identity on it
> and sees the traffic it relays, as any netvm does; a qube that must be
> kept apart from the others gets a dedicated proxy.
<!-- /charter:embed -->

## The template and the packages

The dom0 RPM (`packaging/dom0`) carries the proxy and client packages
(`packaging/proxy`, `packaging/client`) as a bundle. On its first apply
the tool clones a minimal template into its own, `torii-fedora-44-minimal`
by default, and installs the proxy package into it from that bundle,
[`Applier.do_template_package`](../dom0/torii/apply.py#L126):
[SW-261127-HSHY](../claims/apply.md#sw-261127-hshy-the-proxy-package-comes-from-the-dom0-packages-bundle-with-no-repository-for-torii). Every proxy qube is an app qube on that template.
The client package goes into a carried qube's template only when asked,
`torii install-client`.

## The proxy qube

`sys-torii` by default, and any qube under `proxies:`. Its netvm is
`sys-firewall` unless the file names another. Two things run in it.

### The guard

[`torii-guard.nft`](../packaging/proxy/torii-guard.nft), loaded by
[`torii-guard.service`](../packaging/proxy/torii-guard.service) before
any network comes up, drops every forwarded packet; a drop-in,
[`qubes-network-requires-guard.conf`](../packaging/proxy/qubes-network-requires-guard.conf),
keeps Qubes' network setup, which turns forwarding on, from starting
without it. The program never loads or changes it.

<!-- charter:embed SW-261127-JWK2 -->
> **[SW-261127-JWK2](../claims/links.md#sw-261127-jwk2-a-guard-loaded-at-boot-keeps-the-proxy-qube-forwarding-nothing-whatever-the-program-does) A guard loaded at boot keeps the proxy qube forwarding nothing, whatever the program does**
>
> Every qube of the proxy template loads a guard ruleset at boot, before
> Qubes sets up its network; if the guard fails to load, neither Qubes'
> network service, which turns forwarding on, nor the program starts. The
> program's rules are a table of their own and never touch the guard. In
> base chains at priority -400 the guard drops every forwarded packet,
> and any packet about to leave with an input interface other than IPv6
> link-local and multicast control traffic (counted and logged as a
> tripwire); drops IPv6 arriving from `vif` interfaces; and drops the
> proxy qube's own connections to other qubes' Qubes addresses.
<!-- /charter:embed -->

The guard also drops IPv6 arriving from the carried qubes, and dom0 turns
Qubes' IPv6 off on every proxy qube: [SW-261127-3Q23](../claims/apply.md#sw-261127-3q23-every-proxy-qube-has-ipv6-off-for-itself-and-every-qube-behind-it).

### `torii-proxy`

One Go program, `proxy/cmd/torii-proxy`, started by
[`torii-proxy.service`](../packaging/proxy/torii-proxy.service) where dom0
has turned the `torii-proxy` service on.

- **Its config** comes from the features dom0 sets, `vm-config.torii*`,
  which the qube reads through QubesDB and cannot write.
  [`qubesdb.Config`](../proxy/internal/qubesdb/qubesdb.go#L42) joins the parts
  and checks their hash; [`run`](../proxy/cmd/torii-proxy/main.go#L89) waits
  for a valid one and [`waitForNewConfig`](../proxy/cmd/torii-proxy/main.go#L165)
  for the next: [SW-261127-XGDY](../claims/apply.md#sw-261127-xgdy-a-proxy-takes-its-config-whole-or-not-at-all-and-a-failed-check-never-replaces-the-running-one).
- **Its identities**, one tailnet device for each qube with an identity and
  each egress, are made by
  [`daemon.Identities`](../proxy/internal/daemon/daemon.go#L61), each a
  [`TSNet`](../proxy/internal/tailnet/tsnet.go#L38) with its own state folder
  in the qube's private volume: [SW-261127-WNGN](../claims/identity.md#sw-261127-wngn-each-identity-is-a-tailnet-device-of-its-own-with-its-own-name-and-state).
- **Its listeners** are opened by
  [`daemon.Start`](../proxy/internal/daemon/daemon.go#L116): the connect socket
  for qrexec calls, the intercept, DNS and UDP ports for routed qubes and
  the nftables rules that send their traffic there
  ([`intercept.Install`](../proxy/internal/intercept/intercept.go#L74)), each
  identity's inbound ports ([`Engine.Start`](../proxy/internal/engine/engine.go#L286)),
  and the admin socket.
- **Its decisions** are made in one place, the
  [`Engine`](../proxy/internal/engine/engine.go#L87), whose entry points are
  the ways a connection arrives:
  [`Engine.HandleQrexec`](../proxy/internal/engine/engine.go#L534),
  [`Engine.HandleIntercepted`](../proxy/internal/engine/engine.go#L779),
  [`Engine.AnswerDNS`](../proxy/internal/engine/engine.go#L807) and
  [`Engine.HandleUDP`](../proxy/internal/engine/udp.go#L69). The rules
  themselves are pure functions in `proxy/internal/policy`:
  [`DecideOutbound`](../proxy/internal/policy/policy.go#L131),
  [`DecideInbound`](../proxy/internal/policy/policy.go#L290) and
  [`ZoneOf`](../proxy/internal/policy/zone.go#L44).
- **Its egresses**: one [`Selector`](../proxy/internal/egress/egress.go#L92)
  an egress, choosing its exit node ([the egress walkthrough](walkthrough-egress.md)).
- **Its log**: every decision is an event,
  [`Log.Emit`](../proxy/internal/events/events.go#L61): [SW-261127-R4CD](../claims/seeing.md#sw-261127-r4cd-every-connection-the-proxy-decides-is-logged-with-its-verdict-and-the-rule-that-made-it).
- **Its status**: [`admin.Handler`](../proxy/internal/admin/admin.go#L23)
  serves status, the log and explain on the admin socket,
  `/run/torii/admin.sock`, mode 0600.

The packages under `proxy/internal`, and the area of `claims/` whose
decisions each carries:

| Package | What it holds | Area |
|---|---|---|
| `policy` | zones, allowlists, inbound ports: the rules, pure | [connections](../claims/connections.md) |
| `engine` | each way a connection arrives, the dial, the relay | [connections](../claims/connections.md), [egress](../claims/egress.md) |
| `dnsx`, `sni`, `socks5` | DNS answers and the names each qube resolved, the TLS server name, SOCKS | [connections](../claims/connections.md) |
| `qrexec`, `intercept` | how a connection reaches the program and who sent it | [links](../claims/links.md) |
| `tailnet`, `egress` | identities, exit nodes | [identity](../claims/identity.md), [egress](../claims/egress.md) |
| `config`, `qubesdb`, `daemon` | the config, read whole; starting it all | [apply](../claims/apply.md) |
| `events`, `admin` | the log and the status | [seeing](../claims/seeing.md) |

### Its qrexec services

Two scripts in `/etc/qubes-rpc`, both running `torii-proxy` in another
mode:

- [`torii.Connect`](../packaging/proxy/torii.Connect) carries one outbound
  connection from a qube with no netvm.
  [`qrexec.ExecService`](../proxy/internal/qrexec/qrexec.go#L131) hands the
  connection to the daemon's socket with the caller qrexec put in its
  environment, the qube dom0 named: [SW-261127-7C3Y](../claims/links.md#sw-261127-7c3y-a-qrexec-caller-is-the-qube-dom0-names-never-one-the-caller-states).
- [`torii.Status`](../packaging/proxy/torii.Status) prints the status and
  the recent log as one JSON object,
  [`statusService`](../proxy/cmd/torii-proxy/main.go#L188), and accepts nothing:
  [SW-261127-B8BM](../claims/seeing.md#sw-261127-b8bm-the-status-service-is-read-only-it-reports-and-changes-nothing-in-the-proxy).

## The carried qubes

A qube's `network:` decides how its traffic reaches the proxy; the links
area holds each way ([links](../claims/links.md)).

- **`network: none`.** No netvm. If its entry allows any outbound
  connection, `torii-client` runs in it (the `torii-client` service and
  feature, from [`derive.client_config`](../dom0/torii/derive.py#L169)) and
  listens on loopback for its SOCKS port, HTTP proxy and forwards; each
  connection becomes one `torii.Connect` call,
  [`listenAll`](../proxy/cmd/torii-client/main.go#L118): [SW-261127-N3N8](../claims/links.md#sw-261127-n3n8-in-a-qube-with-no-netvm-the-client-listens-on-loopback-only-one-qrexec-call-per-connection).
- **`network: proxy`.** Its netvm is its proxy qube. Its packets arrive on
  a `vif*` interface, are intercepted, and its Qubes source address names
  it, [`Config.ByAddress`](../proxy/internal/config/config.go#L523):
  [SW-261127-BHHX](../claims/links.md#sw-261127-bhhx-a-routed-qube-is-named-by-its-qubes-source-address-which-qubes-keeps-it-from-forging), resting on the observation that a qube cannot send
  as another ([QubesAntispoof](../claims/observations.md#qubesantispoof)).
- **`network: default` or a netvm's name.** Not carried: torii sets its
  netvm and nothing else.
- **Not in the file**, a disposable included, but attached to a proxy: the
  proxy's `unlisted:` policy, by default refusing everything.

Inbound, to either kind, is a `qubes.ConnectTCP` call from the proxy into
the qube on a listed port; a routed qube's own firewall drops new
connections from its netvm, so it gets the same call.

## The qrexec policy

[`derive.policy`](../dom0/torii/derive.py#L216) writes
`/etc/qubes/policy.d/50-torii.policy` (a mesh's file gets its own name), and
[`derive.deny_policy`](../dom0/torii/derive.py#L317) a
`59-torii-deny.policy` after every mesh's file that denies `torii.Connect`
and `torii.Status` to anyone the allows did not name.

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

The trap: the deny file does not name `qubes.ConnectTCP`. That a qube
cannot call it into another without an allow line rests on Qubes' own
default policy, an observation not yet confirmed on a machine
([QubesConnectTCPDenied](../claims/observations.md#qubesconnecttcpdenied)).

## What crosses each boundary

| From → to | What crosses | Why it can be trusted | Entry |
|---|---|---|---|
| a carried qube → its proxy, qrexec | one connection's bytes, a service argument | dom0 names the caller | [SW-261127-7C3Y](../claims/links.md#sw-261127-7c3y-a-qrexec-caller-is-the-qube-dom0-names-never-one-the-caller-states) |
| a routed qube → its proxy, `vif*` | packets | Qubes drops a forged source | [SW-261127-BHHX](../claims/links.md#sw-261127-bhhx-a-routed-qube-is-named-by-its-qubes-source-address-which-qubes-keeps-it-from-forging) |
| the proxy → a carried qube | `qubes.ConnectTCP` on a listed port | dom0's policy lists the ports | [SW-261127-T7GG](../claims/connections.md#sw-261127-t7gg-an-inbound-connection-reaches-a-qube-only-on-a-port-its-entry-or-a-service-lists) |
| dom0 → the proxy | the config, as features | a hash over the parts | [SW-261127-XGDY](../claims/apply.md#sw-261127-xgdy-a-proxy-takes-its-config-whole-or-not-at-all-and-a-failed-check-never-replaces-the-running-one) |
| the proxy → dom0 | status and log, as JSON | the one thing dom0 parses from a qube | [SW-261127-B8BM](../claims/seeing.md#sw-261127-b8bm-the-status-service-is-read-only-it-reports-and-changes-nothing-in-the-proxy) |
| the proxy → its netvm | only what the program dials | forwarding is off | [SW-261127-B6Z6](../claims/links.md#sw-261127-b6z6-a-routed-qubes-traffic-reaches-the-program-and-nothing-else-interception-with-forwarding-off) |
| the proxy → the tailnet | each identity, as its own device | the grants are the owner's | [SW-261127-DH6R](../claims/identity.md#sw-261127-dh6r-torii-proposes-grants-for-the-tailnet-and-never-changes-its-policy) |

A compromised proxy qube holds every identity on it and sees what it
relays, as any netvm does; STK-261126-SM33, above, states it, and a qube
that must stay apart from the others gets a proxy of its own.
