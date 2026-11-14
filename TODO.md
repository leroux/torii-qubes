# TODO

## Later

- **Per-qube firewalling, Little Snitch-style.** An `ask` verdict
  (`outbound.internet: ask`): the engine holds a TCP connection while dom0,
  or a GUI qube, asks through a `torii.Ask` qrexec call (never a prompt in
  the proxy qube); UDP and QUIC decided when the name is resolved. "Always"
  answers become learned rules in the proxy, which `torii rules` offers to
  write into the file. Rules attach to the qube (or a proxy's `unlisted:`),
  never to an identity or an egress.
- **Strict protocols on allowlist entries** (e.g. `pypi.org:443/tls`, or a
  qube's `protocols: [tls, http]`). Today an intercepted connection whose
  first bytes aren't TLS passes on the looked-up name alone, so raw TCP or
  plain HTTP to a shared (CDN) address can reach another site there. Strict
  entries would refuse anything not readable: TLS must carry a matching
  server name, plain HTTP a matching `Host`. The peek in `engine.go`
  (`sni.Peek`) is the hook. Opt-in, since it breaks SSH, databases and
  custom protocols; UDP refused at first (QUIC's server name is readable
  from the Initial packet, later); ECH hides the real name and is refused.
  Doesn't stop fronting by `Host` inside TLS; that needs the next item.
- **TLS interception, opt-in per qube.** The only full answer to domain
  fronting (`Host` inside TLS differing from the server name). Heavy: a
  torii CA in each qube's template, pinned apps break, and `sys-torii` holds
  plaintext. Only if a real need appears.
- **Shared tailnet identities**, explicit (`identities:`) and for listed
  qubes only, if a real need appears; egress identities stay internet-only.
- **Offline disposables through an egress** (the client over qrexec): the
  proxy sees `disp1234` and can't tell its template.

- **Tailnet outbound for a qube on its own netvm, over qrexec** ("option 2").
  A qube with `network: default` or `network: <netvm>` keeps that netvm for
  the internet, and gets the client's SOCKS port, HTTP proxy and forwards for
  tailnet destinations only, through its own identity in the proxy. Not
  transparent: programs must use the proxy or a forward. The proxy program
  already supports it (a qrexec workload whose qube has a network card);
  the machine file has no way to say it. Until then, the transparent way is
  a proxy in front of that netvm: `proxies: [{name: sys-torii-vpn, netvm: sys-vpn}]`
  and `network: proxy` with `proxy: sys-torii-vpn`.
- **Services as real Tailscale Services** (`svc:chat`): today each service
  is its own tsnet device; the file would not change.
- `torii check` lists tailnet devices left behind by qubes and services that
  left the file.
- `torii sign-in-key` restarts only the identity it concerns, not the whole
  proxy.
- Measurements: memory per identity (throughput and load are in `docs/performance.md`).
- A GUI: a read-only Connectivity window and a tray widget first.

## Tried on a real machine

- Egress, 2026-11-11, on the lab (`dev/headscale`, `torii apply` from dom0,
  `torii-routed` behind `sys-torii-test`, exit nodes `torii-peer` and
  `torii-peer2` as real `tailscaled`): healthy after sign-in; a hard-killed
  exit node failed over in 14 s, back after the 120 s hold-down (131 s);
  with both killed, connections refused in under 20 ms, logged
  `egress-no-healthy-exit`, the exit node kept, and a capture at the netvm
  showed no packet from the proxy qube to the destination. On Tailscale
  (`TestTailscaleEgress` with `TORII_TS_EXIT`, a laptop as the second exit
  node): failover 35 s, failback after the hold-down.
- Found there: tsnet uploaded client logs to `log.tailscale.com` and its
  port mapper broadcast SSDP, both straight from the proxy qube. Now both
  off by default (`tailnet.tailscale-logs: yes` turns the logs back on).

## Not yet tried on a real machine

- An unlisted disposable through `sys-torii-dvm`.
- `identity: login`, dedicated proxies (`proxies:`, `proxy: <name>`),
  `network: default` and `network: <netvm>`, `local-inbound`, adoption
  records and refusals (`adopt: force`, `disposables: yes`).
- Qubes 4.2.
- In the simulation (`proxy/internal/engine/sim_test.go`): UDP cells, DNS
  questions as cells, and the `local-inbound` and qrexec-policy cells (dom0's,
  not the engine's).
- Live checks after changes to the guard, the nftables rules, QubesDB use,
  packaging or the dom0 tool's apply: by hand (`docs/testing.md`).

## Rollout

- A GitHub release with the RPM attached, and `make release`.
- CI running `make test` and the model's tests on pull requests.

## Small

- `docs/design.md` says the server-name check "stops domain fronting with
  an allowed name". It stops a mismatched server name, not the classic form
  (allowed server name, other `Host` inside TLS). Reword.
- `docs/reference.md` gives `outbound.tailnet` as `yes | no`, but the machine
  file also takes a list of devices (`dom0/torii/machine.py`, `tailnet_allow`).
  Document it.

- The first `apply` after an upgrade turns the proxy's IPv6 feature off one
  step after restarting it; the guard covers the gap, but the order should
  be swapped.
- The test tailnet (`dev/headscale/`) is set up by hand: a script for it, and
  a way for the tool to sign identities in on Headscale by itself.
