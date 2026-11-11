# Reference

Every command, every key in the machine file, what the tool owns, and the
limits. For a walkthrough, see [`setup.md`](setup.md); for the tailnet side,
[`tailscale.md`](tailscale.md).

## Commands

All run in dom0. `-f FILE` picks the machine file (default `~/lintel.yaml`).

| Command | What it does |
|---|---|
| `lintel plan [--diff]` | Every change `apply` would make, in order; `--diff` shows the policy file's diff |
| `lintel apply [-y]` | Makes those changes, after asking (`-y`: without asking). Stops at the first failing step; run it again after fixing the problem, and it picks up where it stopped |
| `lintel show [QUBE] [--json]` | What each managed qube can reach and what can reach it, read from the live system |
| `lintel check` | Where the live system and the file disagree |
| `lintel log QUBE` | Its recent connection decisions: allowed, refused, and why |
| `lintel log --egress NAME` | An egress's events: exit-node switches, nodes going down and coming back |
| `lintel grants` | Proposed `tagOwners` and grants for the tailnet policy |
| `lintel sign-in-key NAME < key` | Signs an identity in with a one-time auth key, which is stored only in the proxy qube. NAME is a qube, a service, or `egress:<name>` (`egress:<name>@<proxy>` when several proxies carry it) |
| `lintel install-client TEMPLATE` | Installs the client package that qubes with no network card need for outbound access |
| `lintel snapshot` | Prints the live state that `plan` reads (for bug reports and tests) |

`show` reads the live system, not the file: prefs, every policy file (hand-
written ones too), the config each proxy holds, and the identities' state.
`check` reports drift from the file, a proxy that's down or runs an older
config, identities not signed in, and hand-written policy that widens what
the tool set.

lintel only changes the qubes listed under `qubes:`. The first `apply` that
lists a qube records the netvm it had (features `lintel-prior-netvm` and
`lintel-adopted`, written once). A qube removed from the file gets no netvm at
the next `apply`, failing closed; `plan` names the netvm it had before lintel
and the command to restore it, but never restores it itself. A qube that only
hosts services, and isn't listed under `qubes:`, is never touched. Tailnet
devices stay in the admin console until you remove them.

## The machine file

| Key | Default | Meaning |
|---|---|---|
| `proxy` | `sys-lintel` | The proxy qube: a name, or `{name, netvm, label, unlisted}` (netvm `sys-firewall`). |
| `proxy.unlisted`, `proxies[].unlisted` | `refuse` | Qubes attached to that proxy but not listed under `qubes:`, disposables included: `refuse`, or `{internet, egress}` (`internet` as in `outbound.internet`). Never a tailnet identity. |
| `egress.<name>` | | A way out through exit nodes: `exits` (in order, required), `failback-after` (`2m`), `tags` (`[egress]`), `hostname` (`<machine>-egress-<name>`; `-<proxy>` is added on other proxies), `probe` (`host:port`, none). See [`tailscale.md`](tailscale.md#exit-nodes). |
| `proxies` | | More proxy qubes, chosen per qube with `proxy: <name>`. A proxy qube is created once something uses it; the main proxy is never removed by `apply`, since it holds identities. |
| `template`, `base-template` | `lintel-<base-template>`, `fedora-44-minimal` | The tool's own template and what it's cloned from. A new base is a new template: `apply` creates it, moves the proxies onto it (one restart each) and removes the old one once nothing uses it. |
| `tailnet.control-url` | Tailscale | A Headscale (or other) coordination server. |
| `tailnet.tag-owners` | `[autogroup:admin]` | For `lintel grants`. |
| `status-readers` | | Admin qubes that may read `lintel.Status` besides dom0. |
| `defaults` | | Any per-qube key below except `forwards` and `local-inbound`. |

Per qube:

| Key | Values | |
|---|---|---|
| `network` | `none` \| `proxy` \| `default` \| a netvm | **Required**, in the entry or in `defaults`. Where its network comes from. `none`: no netvm, qrexec only. `proxy`: netvm is its proxy qube. `default`: Qubes' default netvm. A netvm's name: that netvm. Only `none` and `proxy` can have a tailnet identity, `outbound:` and forwards. |
| `proxy` | a proxy qube | Which proxy handles it: its netvm when routed, its qrexec target otherwise, and where its identity lives. Default: the main `proxy:`. |
| `outbound.internet` | `none` \| `all` \| a list | Public addresses. List entries: `name`, `*.domain`, address or range, each with an optional `:port` and an optional `/tcp` or `/udp`. **An entry without a protocol is TCP only;** UDP (QUIC, DNS, VPNs) needs `/udp`: `example.com:443/udp`. A raw address nobody resolved and no entry covers is refused. The same entries work in `outbound.lan` and `outbound.tailnet`. |
| `outbound.lan` | `none` \| `all` \| a list | This machine's private network (`192.168/16`, `172.16/12`, `10/8` outside Qubes', `fc00::/7`), never through an exit node. Default `none`: `internet: all` does not include it. |
| `outbound.tailnet` | `yes` \| `no` | May open connections to the tailnet; where to is the grants'. |
| `egress` | `direct` \| an egress | Its internet and DNS through that egress's exit nodes, failing closed. Needs `outbound.internet`. LAN traffic still goes direct. |
| `tailnet.identity` | `none` \| `tagged` \| `login` | Tagged (owned by its tags), or signed in as a person. |
| `adopt` | `force` | Lets the first `apply` move it off a netvm lintel doesn't know (not none, Qubes' default, or a lintel proxy), such as `sys-whonix` or a VPN qube. Without it, `plan` refuses. |
| `disposables` | `yes` | Required to list a disposable template (`template_for_dispvms`): its netvm becomes every disposable's made from it. |
| `tailnet.tags`, `tailnet.hostname` | | Tags for a tagged identity (else `defaults.tailnet.tags`); hostname defaults to the qube's name. |
| `tailnet.inbound` | ports, or `{port, target, tls}` | Ports on the identity, the qube's port behind each, HTTPS terminated if `tls`. |
| `forwards` | `name: {to: host:port, port: N}` | Fixed destinations, on `127.0.0.1:N` in the qube (qubes with no network card). |
| `socks`, `http-proxy` | port, or `no` | The client's ports (1080, 3128) when the qube has outbound access. |
| `local-inbound` | `[{port, from: [qubes]}]` | Qubes on this machine that may reach a port. |

Services (top level, `services:`):

| Key | Values | |
|---|---|---|
| `qube` | a qube | The qube that hosts it. Its network is not changed unless it is also listed under `qubes:`. |
| `ports` | ports, or `{port, target, tls}` | Ports on the service, the qube's port behind each, HTTPS terminated if `tls`. |
| `tags`, `hostname` | | Its tags (else `defaults.tailnet.tags`); its tailnet name defaults to the service's name. |
| `proxy` | a proxy qube | Which proxy holds it (default: `proxy:`). |

## What the tool owns

- **Creates, changes and deletes:** its template (`lintel-fedora-44-minimal`), the proxy qubes
  (tagged `lintel-owned`), and `/etc/qubes/policy.d/50-lintel.policy`. Don't
  hand-edit that file; `apply` replaces it.
- **On your qubes, only:** netvm, the `lintel-client` service, the feature
  `vm-config.lintel-client`, and the tags `lintel-managed` and
  `lintel-netvm-kept`.
- **Reads, never writes:** every other policy file. Grants to
  `qubes.ConnectTCP` or `lintel.*` written by hand show up in `show` and
  `check`.
- **The proxy's config** goes to it as the features `vm-config.lintel*`
  (compressed and split, since QubesDB caps a value under 3 KB). The proxy
  reloads when they change.

## Troubleshooting

- **In the proxy qube:** `journalctl -u lintel-proxy`, and
  `/usr/lib/lintel/lintel-proxy status|log|explain -workload Q -dest host:port`.
- **A networked qube can't resolve a tailnet short name** such as `peer`.
  Use the full name (`peer.tailXXXX.ts.net`). Fedora's `systemd-resolved`
  doesn't send single-label names to the DNS server, and Qubes can't push a
  search domain.
- **"connected" then closed, for a networked qube:** connections are
  intercepted, so the connection opens first and closes when the proxy
  refuses or can't reach the destination. `lintel log QUBE` says which.
- **The proxy qube can't shut down:** a netvm with running clients won't.
  `apply` detaches and reattaches them when it must restart the proxy.

## Limits

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
  `default_dispvm` whose netvm isn't a lintel proxy; lintel never sets
  `default_dispvm` itself.
- With an egress, LAN traffic (`outbound.lan`) still goes direct through the
  proxy's netvm; `plan` notes it. Connections open through an exit node break
  when the egress switches to another one: the public address changes.
- One tailnet per machine file (one coordination server).
- Not built yet: removing tailnet devices from `check`; a GUI; Tailscale
  Services; measurements of memory per identity and userspace throughput.
