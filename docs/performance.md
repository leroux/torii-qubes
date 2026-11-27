# Performance and limits

Measured on one Qubes 4.3 machine (a laptop, 2 vCPUs per qube, the proxy
qube at its default 400 MB, balanced up to 4 GB), November 2026, against the
test mesh on local Headscale (`dev/headscale/`). Rough numbers: what to
expect, not a benchmark.

## What one machine needs

A workstation's peak, roughly: 1,000 to 2,000 open connections (a busy
browser holds 200 to 500), bursts of 100 to 200 new connections a second on
page loads and fewer than 10 sustained, and as much bandwidth as its uplink.

## Measured

Against real speed-test endpoints (speed.cloudflare.com; the uplink was about
88 Mbit/s down, 12 up), 20 requests each for latency:

| Path | Request (median) | Jitter | Down | Up |
|---|---|---|---|---|
| The netvm itself, direct (baseline) | 109 ms | 16.6 ms | 87 Mbit/s | 12 Mbit/s |
| Routed qube, through the proxy | 92 ms | 8.7 ms | 88 Mbit/s | 12 Mbit/s |
| No network card, qrexec and SOCKS | 141 ms | 20.9 ms | 86 Mbit/s | 12 Mbit/s |
| Routed qube, through a tailnet exit node | 99 ms | 23.8 ms | 75 Mbit/s | 12 Mbit/s |

The uplink is the limit on every path. qrexec adds about 30 ms to each new
connection (one qrexec call per connection); the exit node costs some
download (its traffic crosses the relay).

Against a server on the netvm, to find the proxy's own ceiling:

| Path | 1 stream down / up | 4 streams down / up |
|---|---|---|
| Routed qube, through the proxy | 2,280 / 558 Mbit/s | 1,822 / 1,112 Mbit/s |
| No network card, qrexec and SOCKS | 1,025 / 468 Mbit/s | 1,330 / 838 Mbit/s |

Between qubes over the tailnet (always through the relay on one machine:
Qubes blocks the direct path), to a peer:

| Path | 1 stream down / up | 4 streams down / up |
|---|---|---|
| Routed qube, the proxy's identity | 51 / 52 Mbit/s | 52 / 60 Mbit/s |
| qrexec forward, the proxy's identity | 53 / 51 Mbit/s | 51 / 62 Mbit/s |

A request to the peer took 8.6 ms (jitter 2.4) from the routed qube and
43.7 ms (jitter 13.5) through the forward. One later stream reached 90 Mbit/s
with no qube on the path busy (the proxy 70% of one core, the relay 44%, the
peer 56%): the ceiling is the path, TCP through two userspace network stacks
and a relay, not one program. Devices on other machines usually get a direct
WireGuard path instead.

The proxy program's peak memory over all of these was 57 MB. The parts each
path crosses are in [`architecture.md`](architecture.md).

In isolation (`go test ./internal/engine -run TestStress -stress 5`): 10,000
connections and 100 transfers of 4 MiB at once on every path pass every
check (every byte, one log event per connection, nothing left open). At
`-stress 25` (50,000 connections, 500 transfers at once, on a 7-core dev
qube that also runs every fake server and hashes every byte) no byte was
wrong, but about 0.2% of connections timed out waiting for the starved fake
servers, and those are counted differently: the machine, not the engine, is
the limit there.

## Why these numbers

The machine: an Intel i7-9750H (6 cores, no hyper-threading under Qubes, 2.6
GHz base), DDR4-2400; each qube 2 vCPUs, PVH. Baselines without the proxy,
with the same tools:

| | Down | Up |
|---|---|---|
| Loopback in one qube (the test server and curl) | 24.8 Gbit/s | 2.8 Gbit/s |
| One Xen network crossing, a qube to its netvm | 3.6 Gbit/s | 1.5 Gbit/s |
| ChaCha20-Poly1305 (WireGuard), one core, 1,280-byte packets | 3.9 Gbit/s | |
| AES-128-GCM (the relay's TLS), one core | 6.7 Gbit/s | |

- **Routed through the proxy, 2.3 Gbit/s down.** Two network crossings
  instead of one, and the proxy qube does a backend's copying for the
  workload's card as well as its own sending, on 2 vCPUs: about 60% of one
  crossing (3.6) is what to expect. Memory (19 GB/s) is far from the limit;
  Xen's per-packet copying is. Upload is capped by the tools (1.5 Gbit/s on
  one crossing); the proxy gets 0.6 (one stream) to 1.1 (four).
- **qrexec, about 1 Gbit/s, and 30 ms more per connection.** Data crosses a
  vchan ring, about 125 MB/s here; each connection is a qrexec call, which
  dom0's policy daemon evaluates and which starts a process: tens of
  milliseconds is usual for qrexec.
- **The tailnet between qubes, 50 to 90 Mbit/s.** Crypto would allow
  gigabits on one core. The limit is the relay: `derper` buffers 32 packets
  per client (`defaultPerClientSendQueueDepth`) and drops the oldest when
  full; it dropped 2.4% of what it sent during these runs. TCP inside
  WireGuard backs off on loss: about MSS / RTT x 1.22 / sqrt(loss), with
  1,200-byte segments, 2.4% loss and a round trip of 1 to 4 ms, gives 20 to
  80 Mbit/s, which is what was measured. Tailscale's relays work the same way:
  they are a fallback; a direct path, where one exists, avoids them.
- **The exit node, 75 of 88 Mbit/s.** Its traffic crosses the relay, near
  the relay's ceiling.

## Limits set at installation

| Limit | Default | Set to | Why |
|---|---|---|---|
| Connection tracking in the proxy qube (`nf_conntrack_max`) | 3,072 (sized from the memory the qube boots with) | 65,536, by [torii-proxy.service](../packaging/proxy/torii-proxy.service) | Each routed connection holds an entry for two minutes after it closes: the default allowed about 25 new connections a second, and live it filled and dropped packets. 65,536 is what Linux gives a desktop with a few GB and OpenWrt a larger router; about 540 a second sustained, about 20 MB if full |
| Open files of the proxy program | 1,024 soft | 524,288 | Go raises the soft limit to the hard one at start; nothing to do |

Not changed: the client qubes' own connection tracking (3,072 too, Qubes'
default, the same as `sys-firewall`'s for every qube today).
