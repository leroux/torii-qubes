# torii: the tsnet proxy program

One Go program per proxy qube, built on `tsnet`, holding one tailnet
identity per workload. It carries the traffic of workloads with no network
card (over qrexec) and of routed workloads (proxy qube as netvm, transparent
interception), and decides every connection against its config. Why it is
built this way: [`../docs/design.md`](../docs/design.md).

The dom0 tool (`../dom0`, see `../docs/reference.md`) writes that config and reads
the program's status. This directory also holds `torii-client`, the small
program in workloads without a network card. The packages are built from
`../packaging`.

## What it does

| Workload | Inbound | Outbound |
|---|---|---|
| qrexec (no network card) | Identity listens on declared ports; each connection is one `qrexec-client-vm <qube> qubes.ConnectTCP+<port>` from proxy to workload. With `tls`, the identity terminates HTTPS with its own certificate first. | Workload calls `torii.Connect+http` (HTTP proxy: CONNECT, and absolute-form plain HTTP checked per request), `torii.Connect+socks` (SOCKS5, names passed through) or `torii.Connect+f.<name>` (fixed forward). dom0 names the caller. `torii-client` in the workload turns loopback ports into these calls. |
| routed (proxy qube is netvm) | Identity listens; each connection is a `qubes.ConnectTCP` call into the workload, as for qrexec workloads (a Qubes app qube's own firewall drops new connections from its netvm). | nftables redirects every TCP connection and all DNS from `vif*` to the program, and hands other IPv4 UDP to it by TPROXY; forwarding is off. The source address names the workload. |
| none (the proxy qube itself) | Identity listens; proxy dials localhost. | A SOCKS port (`listen.local_socks`, loopback) and fixed forwards on loopback ports, decided like a qrexec workload's. |

UDP from routed workloads is decided once per flow (workload address,
destination) and relayed until idle (`UDPIdle`, 60 s). It goes over the
identity for tailnet and exit-node destinations, and over the proxy
qube's netvm otherwise. Replies leave from a transparent socket bound to
the destination the workload sent to, so the workload sees the real
peer. On an allowlist, UDP is decided by the name the workload resolved;
QUIC server names are not read. A workload may hold at most
`MaxUDPFlows` flows (512), refused ones included.

Every decision (`internal/policy`, pure) is one of these:

- **Tailnet**: allowed if `tailnet_outbound`; where it may go is left to
  grants. It is dialled straight into the identity's netstack, never the
  system network.
- **Internet** `none | full | allowlist`: dialled through the proxy qube's
  netvm, or through an **egress** (`egress`): exit nodes tried in order, on
  the egress's own internet-only identity, failing closed.
- **Allowlist**: decided by name. The name is either the one the workload
  asked for (SOCKS or forward) or the one it resolved through the program's
  DNS, remembered per workload. A TLS server name, when sent, must be
  allowlisted too, so domain fronting on a shared address is refused. The
  ClientHello is read with `crypto/tls`, so splitting it across records
  does not hide the name; a connection that opens like TLS but has no
  readable ClientHello is refused (`server-name-unreadable`). One that closes before sending
  anything is logged as `closed` (`closed-before-data`): nothing was relayed. A raw
  IP that nobody resolved is refused.
- **Forbidden on the internet path** regardless of config: loopback,
  link-local, multicast, the Qubes virtual DNS addresses, the tailnet
  ranges (`100.64/10`, `fd7a:115c:a1e0::/48`), and, on the direct path,
  the proxy qube's own addresses, so a workload cannot reach the proxy's
  own sockets. The same check applies to DNS answers, so a public name
  cannot rebind to them.
- **qrexec calls** are accepted only from workloads with `link: qrexec`,
  whatever caller name arrives.

Hooks:

- **Decision log**: one event per decision (source, destination, server
  name, verdict, reason, path, identity), kept in memory and optionally as
  JSONL per workload.
- **Explain**: asks whether a connection would be allowed, without sending
  traffic.
- **Status**: identities, state, sign-in link, assigned tags, exit node,
  active connections.

All three are served on a unix socket. dom0 reads them through
`torii.Status`.

One qube can have several identities: each is a workload of its own
(`"infra.chat"`) with `qube` naming the qube its inbound connections
go to. Such workloads have inbound only.

### Config

`run -config qubesdb:torii` reads the per-qube features dom0 sets,
`vm-config.torii*`. QubesDB refuses a value of 3072 bytes or more (and a
larger one set while the qube is halted makes its start fail), so dom0
stores the JSON gzipped, base64'd and split: `vm-config.torii` holds
`<parts> <sha256>`, `vm-config.torii.0` .. the parts (`internal/qubesdb`).
The program waits for a valid config at start, watches `/vm-config/` with
`qubesdb-watch`, and restarts itself in-process on a config with a new
version. A config that is half-written, or invalid, never replaces the
running one. Identities keep their state in `state_dir` across restarts.

In a Qubes netvm, Qubes' own `ip qubes` input chain rejects what arrives
from downstream for the netvm itself, and an accept in the program's own
table does not override it; `-install-rules` therefore also adds its accepts
to `ip qubes custom-input`, marked `comment "torii"`, replacing its earlier
ones and leaving everyone else's.

## Layout

```
cmd/torii-proxy     run | connect-service | status-service | status | log | explain | rules
cmd/torii-client    in a workload: loopback ports -> qrexec calls, from vm-config.torii-client
internal/config     the proxy qube's config (JSON; dom0 delivers it as feature vm-config.torii)
internal/qubesdb    reads the chunked config from QubesDB; watches it
internal/policy     pure decisions: outbound, DNS names, inbound, allowlist, forbidden addresses
internal/engine     carries decisions: HTTP proxy, SOCKS, forwards, interception, DNS, inbound (TLS), exit nodes, explain, status
internal/tailnet    Identity interface; TSNet (tsnet.Server per workload) and Fake (in-memory)
internal/intercept  nftables rules (and Qubes' custom-input), SO_ORIGINAL_DST, DNS listeners (Linux)
internal/qrexec     torii.Connect socket + exec helper; DialQube via qrexec-client-vm
internal/sni        reads the ClientHello server name without consuming it
internal/socks5     SOCKS5 CONNECT server
internal/dnsx       DNS answers and the per-workload address-to-name cache
internal/events     decision log
internal/admin      status/log/explain over a unix socket
internal/daemon     wires it all from one config
internal/itest      levels 2-4 below
example-config.json  an example config, as dom0 would write it
```

The packages are built from `../packaging` by `make` at the repository root.

## Tests

`make test` runs every
level that can run on the machine.

| Level | What | Command | Needs |
|---|---|---|---|
| 1 | Unit: config, policy, SNI, SOCKS5, DNS, qrexec (a fake `qrexec-client-vm`), engine on `FakeNet` | `make test-unit` | nothing |
| 2 | In-process tailnet: real tsnet nodes, WireGuard, local DERP, `testcontrol`; inbound, SOCKS by MagicDNS name, exit node through a real peer, sign-in link | `go test ./internal/itest -run 'TestRealTailnet\|TestSignIn'` | nothing |
| 3 | Network namespaces as fake qubes: workloads behind `vif*` veths, real daemon, nftables and TPROXY, forwarding off, upstream netvm and DNS. Two checks (UDP replies to a resolved name, inbound into a routed workload) fail in containers and run only with `TORII_NETNS_REAL=1` | `make test-netns` | root, `ip`, `nft` |
| 4 | Local Headscale: two machines' proxies, two people's laptops, tags and **grants**, sign-in approval by tag owner | `make test-headscale` | `headscale` (v0.29.7) |
| sim | Every cell of a machine file through the engine, the config from the dom0 tool | `go test ./internal/engine -run TestSimulatedMachine` | python3 (the dom0 tool) |
| 5 | A Qubes machine (done by hand; see `../docs/testing.md`) | | Qubes 4.3, a test tailnet |

Some of the scenarios checked:

- A port declared in the config but not granted never reaches the program
  (Headscale drops it).
- Across two machines, only the granted person reaches the database.
- A job reaches the database by MagicDNS name over SOCKS, but not the
  server.
- Fronting with an allowed name to SOCKS and another name in TLS is refused,
  also with the ClientHello split across TLS records.
- A routed qube's address that it never resolved is refused.
- Unknown sources are refused.
- UDP is relayed, not forwarded: the upstream server sees the proxy's
  address. The workload's connected socket accepts the reply, so the
  reply comes from the real peer address.
- UDP to an address the workload never resolved is refused, and so is
  UDP to the tailnet without `tailnet_outbound`.
- Tailnet UDP crosses real WireGuard.
- The proxy qube's own SOCKS port and forwards reach the internet
  (allowlisted) and the tailnet.
- With the exit node gone, connections fail and the proxy's own network
  dialler is never used (fail closed).
- When the coordination server assigns tags other than the config declares,
  status reports the mismatch.

## Findings

- **Sign-in by link for tagged devices** works on **Tailscale** (level 5:
  an admin who owns the tag opened the link, and the device joined with the
  tag) and on **Headscale**: an identity that advertises `tag:server` prints a
  `/register/…` link, and `headscale auth register --user alice` succeeds when alice
  owns the tag in `tagOwners`; the node becomes tagged and owned by
  `tagged-devices`. When the approving user doesn't own the tag, the
  approval is refused.
- **Exit routing fails closed** holds structurally. Identity dials go into
  the identity's gVisor netstack (`DialContextTCPWithBind`), not through
  `tsnet.Server.Dial`. That method falls back to the system network for any
  address without a tailnet route, so using it for outbound would have
  leaked. Internet names on the exit path are looked up with DNS over TCP to
  `exit_dns`, through the identity, never the local resolver. Confirmed at
  level 5 against a real exit node. Captive portals are not handled.
- **Keys carry their tags.** Headscale refuses advertised tags on any
  pre-auth-key registration; Tailscale takes the key's tags. So the program
  advertises tags only when signing in by link.
- **A tsnet node is not an exit node by itself.** It resets traffic for
  routes it advertises unless it has a fallback handler. The test's exit
  node has one; real exit nodes are tailscaled.

## Confirmed on Qubes 4.3 (level 5)

- qrexec-agent writes `<service>+<arg> <caller>\0` to socket-based
  services, as `internal/qrexec` assumes. The packaged `torii.Connect` is
  the exec helper, which sends the same header.
- `vm-config.*` features read as `qubesdb-read /vm-config/<key>`, under
  3072 bytes per value (see Config). A qube can overwrite its own QubesDB
  copy; dom0's value stays.
- Qubes' anti-spoofing on `vif*` holds: with the workload's own masquerade
  removed, a spoofed source address is dropped in the proxy qube's
  `ip qubes antispoof` chain, so the program can trust the source address.
- nftables: the program's `inet torii` table runs beside Qubes' tables; it
  needs its accepts in `ip qubes custom-input` too (see Config).

## Known limits

- An allowlist is checked by name: the name asked for or resolved, plus
  the TLS server name when sent. Through the HTTP proxy, plain HTTP is
  decided per request by the URL's host, one host per connection. Raw
  plain HTTP or QUIC to an address shared by an allowed name (a CDN) can
  still reach another site on it with a different `Host` header.
- UDP for routed workloads is IPv4 only; IPv6 UDP is dropped.

## Not built yet

- SOCKS UDP ASSOCIATE. It does not fit qrexec: a workload with no
  network card has no UDP socket to send from.
- Inbound UDP on identities.
- `local-inbound`: that is dom0 policy, not this program.
- Throughput and per-identity memory measurements.
- A kernel fast path.
- Tailscale Services.
