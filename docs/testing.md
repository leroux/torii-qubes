# How torii is tested

`make test` runs everything below except the root-only and Headscale levels,
in seconds, with no Qubes machine and no tailnet account.

| What | Where | What it shows |
|---|---|---|
| The dom0 tool | `dom0/tests/` | Machine-file parsing and every error; plans golden-tested from a snapshot recorded on Qubes 4.3; a second apply plans nothing; a qube leaving the file is reset; every yaml block in the README and docs parses |
| Decisions | `proxy/internal/policy` | Zones, allowlists (names, addresses, ranges, ports), the tailnet list, server names, inbound |
| The engine | `proxy/internal/engine` | Each path (intercept, DNS, SOCKS, HTTP proxy, forwards, inbound, TLS inbound, exit nodes, UDP) against a fake tailnet and internet |
| **The simulated machine** | `proxy/internal/engine/sim_test.go` | Every cell (source x way out x destination, and every inbound port) of `testdata/sim/machine.yaml`, through the real engine (below) |
| Load | `proxy/internal/engine/stress_test.go` | Large transfers on every path at once with thousands of short connections in between: every byte both ways, one event per connection, nothing left open; `-stress N` scales it. Live numbers: [`performance.md`](performance.md) |
| Config across languages | `proxy/internal/qubesdb` | The Go reader decodes what the dom0 tool writes into QubesDB |
| In-process tailnet | `proxy/internal/itest` | Real tsnet nodes, WireGuard and a test coordination server: inbound, SOCKS by name, exit nodes, sign-in links |
| Network namespaces (root) | `make -C proxy test-netns` | Workloads behind veths with the real daemon, nftables and TPROXY |
| Local Headscale | `make -C proxy test-headscale` | Two machines, people, tags and grants |

## The simulated machine

`sim_test.go` takes the machine file through the dom0 tool's own `derive`
(Python), so the engine gets the config Qubes would. Around the engine
everything is fake: the tailnet (grants from `testdata/sim/tailnet-policy.json`),
the internet, an exit node's internet, the LAN and the qubes. Each
destination answers with a marker only it serves; a cell is reached when
its marker comes back through the engine. The expected verdict is the
engine's own decision (`Explain`) and, for tailnet destinations, the grants;
traffic through an exit node must come back from the exit node's side.

It catches a path that does not enforce what was decided (checked by breaking
the exit path and the grants: the run fails on the cells concerned). It does
not catch a decision rule that is itself wrong; the policy package's tests
cover those rules.

## What only a real machine shows

None of the above exercises Qubes or Tailscale themselves: Qubes' own
firewall tables next to the proxy's, the boot order of the guard and Qubes'
network setup, QubesDB's limits, real qrexec policy, Tailscale's grants,
tags, sign-in, HTTPS certificates and MagicDNS, the dom0 tool against real
qubes, and the packages in real templates. Most bugs found so far were
there. They were checked live on a Qubes 4.3 machine against Tailscale in
November 2026 (291 cells of a seven-qube machine file). The scripts that produced them were
removed in favour of the simulation; a change in those areas needs a check
on a real machine by hand.
