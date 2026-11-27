# torii: the tsnet proxy program

One Go program per proxy qube, built on `tsnet`, holding one tailnet
identity per workload. It carries the traffic of workloads with no network
card (over qrexec) and of routed workloads (proxy qube as netvm, transparent
interception), and decides every connection against its config. Why it is
built this way: [`../docs/design.md`](../docs/design.md).

The dom0 tool (`../dom0`, see `../docs/setup.md`) writes that config and reads
the program's status. This directory also holds `torii-client`, the small
program in workloads without a network card. The packages are built from
`../packaging`.

What it is made of, what runs where and what crosses each boundary is in
[`../docs/architecture.md`](../docs/architecture.md); how one connection is
decided, step by step, in
[`../docs/walkthrough-connection.md`](../docs/walkthrough-connection.md).
The rules themselves are in [`../claims/`](../claims/needs.md).

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
