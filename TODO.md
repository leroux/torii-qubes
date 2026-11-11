# TODO

## Later

- **Tailnet outbound for a qube on its own netvm, over qrexec** ("option 2").
  A qube with `network: default` or `network: <netvm>` keeps that netvm for
  the internet, and gets the client's SOCKS port, HTTP proxy and forwards for
  tailnet destinations only, through its own identity in the proxy. Not
  transparent: programs must use the proxy or a forward. The proxy program
  already supports it (a qrexec workload whose qube has a network card);
  the machine file has no way to say it. Until then, the transparent way is
  a proxy in front of that netvm: `proxies: [{name: sys-lintel-vpn, netvm: sys-vpn}]`
  and `network: proxy` with `proxy: sys-lintel-vpn`.
- **Services as real Tailscale Services** (`svc:chat`): today each service
  is its own tsnet device; the file would not change.
- `lintel check` lists tailnet devices left behind by qubes and services that
  left the file.
- `lintel sign-in-key` restarts only the identity it concerns, not the whole
  proxy.
- Measurements: memory per identity (throughput and load are in `docs/performance.md`).
- A GUI: a read-only Connectivity window and a tray widget first.

## Not yet tried on a real machine

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

- The first `apply` after an upgrade turns the proxy's IPv6 feature off one
  step after restarting it; the guard covers the gap, but the order should
  be swapped.
- The test tailnet (`dev/headscale/`) is set up by hand: a script for it, and
  a way for the tool to sign identities in on Headscale by itself.
