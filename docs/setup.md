# Setting up a machine

From nothing to one service, in a qube with no network card, reachable from your tailnet, then the
other patterns, one at a time. Plan on an hour the first time; most of it is
the first `apply` (downloading and preparing a template).

The tailnet side (tags, grants, HTTPS, sign-in) is in
[`tailscale.md`](tailscale.md); this guide links there at each step that
needs it. Every command and key is at the end of this guide, under
[Commands](#commands) and [The machine file](#the-machine-file); what torii
may touch is under [What torii owns](#what-torii-owns). What happens inside
each step is in the walkthroughs ([`walkthrough-apply.md`](walkthrough-apply.md)).

## What you need

- A Qubes 4.3 machine (4.2 should work but is untested), and a dom0 terminal.
- A qube with network to build in, here `dev`. Any Fedora or Debian app qube
  with `git`, `make` and Go 1.21 or newer (`sudo dnf install golang` or
  `sudo apt install golang` in its template).
- Admin rights on a Tailscale tailnet, or someone who has them for the steps
  in `tailscale.md`. A free personal tailnet is fine for trying it out.

## 1. Build the package

In `dev`, in your home directory:

```sh
git clone https://github.com/leroux/torii-qubes.git && cd torii-qubes
make
```

The first `make` fetches the Go toolchain the code needs and the packaging
tool, nfpm, at pinned versions, so it takes a minute or two. Clone in full
(not `--depth 1`): the package version counts commits, and a shallow clone
builds one that looks older than what you have installed.

Expect it to end with:

```
created package: ../../build/torii-dom0-0.1.0.N-1.noarch.rpm
```

`make test` runs the unit tests if you want to check the build (it needs
`python3-pyyaml`, or `python3-yaml` on Debian). The RPM is the only file a machine needs, so you can also build it once
and copy it to other machines.

## 2. Install it in dom0

In dom0 (replace `dev` with your build qube, and `~/torii-qubes` with the
checkout if you cloned elsewhere):

```sh
qvm-run --pass-io dev 'cat ~/torii-qubes/build/torii-dom0-*.noarch.rpm' > /tmp/torii-dom0.rpm
sudo dnf install /tmp/torii-dom0.rpm
torii --version
```

dom0 has no network, so `dnf` installs from the file only. The package
depends on `python3-qubesadmin` and `python3-pyyaml`, which dom0 already has.

## 3. Prepare the tailnet

Before the first identity joins, its tag must exist in the tailnet policy,
with you as an owner. With `machine: m1` and the short tag `server` below, that is
`tag:m1-server`, and every tagged device also carries `tag:m1`; own both. See
[`tailscale.md`, "Tags"](tailscale.md#tags). One line in `tagOwners` is
enough for now; grants come in step 7.

## 4. Pick a qube and write the file

Pick an app qube that runs a service on a local port, here `wiki` with a web
server on port 8080, listening on `127.0.0.1` or `0.0.0.0` inside the qube.
It may have a netvm now; the tool will take it away.

In dom0, `~/torii.yaml`:

```yaml
machine: m1
proxy: sys-torii
defaults:
  network: none
  tailnet: { tags: [server] }
qubes:
  wiki:
    tailnet: { identity: tagged, inbound: [{port: 80, target: 8080}] }
```

That reads: `wiki` has no network card and no internet, and has a tailnet
identity `m1-wiki` tagged `tag:m1-server` and `tag:m1`. Port 80 on that identity leads to port
8080 in the qube. A file with one wrong key is refused whole
([SW-261127-9BNW](../claims/apply.md#sw-261127-9bnw-the-machine-file-is-read-whole-and-refused-whole-when-a-key-a-network-or-a-value-is-wrong)); the tags' machine prefix is [SW-261127-P8H7](../claims/identity.md#sw-261127-p8h7-torii-must-give-a-tagged-identity-exactly-the-files-tags-prefixed-by-the-machine-and-torii-check-must-name-any-other).

## 5. Plan

```sh
torii plan
```

On a machine that has never run torii, expect this (numbers differ):

```
fedora-44-minimal: download the base template (qvm-template install)
torii-fedora-44-minimal: create the proxy template, a clone of fedora-44-minimal
torii-fedora-44-minimal: install the proxy package 0.1.0.N
sys-torii: create the proxy qube (template torii-fedora-44-minimal, netvm sys-firewall, provides network)
sys-torii: set netvm = sys-firewall
sys-torii: set provides_network = True
sys-torii: set autostart = True
sys-torii: enable service torii-proxy
sys-torii: set its config 1a2b3c4d5e6f: 1 workload
sys-torii: start it and wait until it runs config 1a2b3c4d5e6f
50-torii: write the qrexec policy
wiki: set netvm = (none) (was sys-firewall)
wiki: tag torii-managed
wiki: set feature torii-prior-netvm = sys-firewall
wiki: set feature torii-adopted = 2026-11-10

15 changes. Run `torii apply` to make them.
```

`torii plan --diff` also shows the policy file it would write. If `plan`
refuses with `cannot apply ... to this machine`, the message says why: a qube
that isn't there, a netvm named as a workload, a `sys-torii` you made
yourself, and so on. Two refusals protect setups torii can't judge: a qube
on a netvm it doesn't know (`sys-whonix`, a VPN qube) needs `adopt: force`
before torii moves it, and a disposable template needs `disposables: yes`.
The full list of refusals is [SW-261127-2JH3](../claims/apply.md#sw-261127-2jh3-plan-refuses-before-any-step-a-file-that-names-what-this-machine-cannot-be-given).

## 6. Apply

```sh
torii apply
```

It lists the changes, asks, then numbers each step as it goes:
`[1/13] fedora-44-minimal: download the base template ...`. The first three
steps take most of the time: downloading the template (a few hundred MB),
cloning it, and installing the proxy's packages into it through the updates
proxy. After that it creates and starts `sys-torii` and waits until the
proxy program reports the new config. It ends with `Done.`.

If a step fails, `apply` stops there:

```
apply stopped: torii-fedora-44-minimal: install the proxy package 0.1.0.N: installing the proxy's dependencies failed (exit 1): ...
Fix it and run `torii apply` again; finished steps are kept.
```

Fix the cause (here: the template's updates proxy) and run `torii apply`
again. It starts where it stopped ([SW-261127-63CS](../claims/apply.md#sw-261127-63cs-apply-makes-the-plans-steps-in-order-stops-at-the-first-that-fails-and-a-second-run-continues)). The wait for the
proxy is [SW-261127-QMVP](../claims/apply.md#sw-261127-qmvp-apply-goes-on-past-a-proxys-config-only-once-the-proxy-reports-running-it).

## 7. Sign the identity in, and grant access

```sh
torii show
```

```
proxy sys-torii: running, config 1a2b3c4d5e6f, 1 workloads
  waiting for sign-in: wiki: https://login.tailscale.com/a/0123456789abcd

wiki  (no network card, serves the tailnet)
  network card   no
  proxy qube     sys-torii (qrexec)
  identity       m1-wiki (tagged tag:m1-server,tag:m1) NeedsLogin
  tailnet in     80->8080 (who: the tailnet's grants)
  tailnet out    no
  internet       none
```

Open the link in a browser where you're signed in to the tailnet as an owner
of `tag:m1-server` and `tag:m1`, and approve it. To skip the browser, use a one-time key
instead (`torii sign-in-key wiki < key.txt`); both are in
[`tailscale.md`, "Signing identities in"](tailscale.md#signing-identities-in).
A few seconds later `torii show` says `Running 100.x.y.z`. Either way, the
key or the identity's state is kept only in the proxy qube
([SW-261127-3TPX](../claims/identity.md#sw-261127-3tpx-an-identity-must-sign-in-by-a-link-a-person-approves-or-by-a-one-time-key-that-only-the-proxy-qube-keeps)).

Then let people in. `torii grants` prints a proposal:

```json
{
  "tagOwners": { "tag:server": ["autogroup:admin"] },
  "grants": [ { "src": ["autogroup:member"], "dst": ["tag:server"], "ip": ["tcp:80"] } ]
}
```

Narrow `src` if not every member should reach it, and paste it into the
tailnet policy ([`tailscale.md`, "Grants"](tailscale.md#grants)). torii
never changes the tailnet's policy itself ([SW-261127-DH6R](../claims/identity.md#sw-261127-dh6r-torii-must-only-propose-grants-for-the-tailnet-and-must-not-change-the-tailnets-policy)).

## 8. Check it

From a laptop on the tailnet, open `http://wiki` (or
`http://wiki.<your-tailnet>.ts.net`). It should show the service.

On the machine:

```sh
torii check     # OK: the live system matches /home/user/torii.yaml
torii log wiki  # one line per connection: who, allowed or refused, why
torii plan      # No changes: the machine matches /home/user/torii.yaml
```

What `check` looks for is [SW-261127-SMYC](../claims/seeing.md#sw-261127-smyc-check-names-each-difference-it-looks-for-between-the-live-machine-and-the-file-and-fails-on-one); what the log holds,
[SW-261127-R4CD](../claims/seeing.md#sw-261127-r4cd-every-connection-the-proxy-decides-is-logged-with-its-verdict-and-the-rule-that-made-it).

If the laptop can't connect:

- **Nothing in `torii log wiki`:** the connection never reached the machine.
  Check the grant, and that the laptop and `wiki` are on the same tailnet.
- **`allow` in the log with an error:** the service in `wiki` isn't
  listening on 8080.
- **`waiting for sign-in` still in `torii show`:** step 7 isn't done.

## 9. Add the other patterns

Each is a few lines in the file and another `plan` / `apply`. The examples
in [`examples/machine.yaml`](../examples/machine.yaml) show each in full.

**A qube with no network card that fetches packages and reaches the tailnet**

```yaml
  job-1:
    outbound: { internet: [pypi.org, files.pythonhosted.org], tailnet: yes }
    tailnet: { identity: tagged }
```

`plan` will note that `job-1`'s template lacks the client:

```
note: job-1 needs the torii client in fedora-43-xfce; install it with `torii install-client fedora-43-xfce`, then restart them
```

Run that, then `torii apply`, then restart `job-1`. Inside it, programs that
honour `HTTPS_PROXY` (pip, curl, git, most others) just work for the
allowlisted names. `ALL_PROXY` points at SOCKS for the rest, and
`curl -x http://127.0.0.1:3128 https://wikipedia.org` is refused. How an
entry matches is [SW-261127-2XS6](../claims/connections.md#sw-261127-2xs6-the-proxy-must-match-an-allow-entry-by-exact-name-or-subdomain-port-and-protocol-treating-an-entry-without-a-protocol-as-tcp-only); the client listens on loopback only
([SW-261127-N3N8](../claims/links.md#sw-261127-n3n8-torii-client-must-listen-on-loopback-only-and-must-make-one-qrexec-call-per-connection)).

The client package does nothing in qubes where dom0 hasn't enabled it, so
installing it in a shared template is harmless.

**A networked qube behind the proxy**

```yaml
  browser:
    network: proxy
    outbound: { internet: [example.com, "*.example.com"], tailnet: yes }
    tailnet: { identity: tagged }
```

Its netvm becomes `sys-torii`. Inside it, reach tailnet devices by their
full name (`laptop.<your-tailnet>.ts.net`); short names don't resolve from
Fedora qubes.

**Services: named HTTPS endpoints on a qube**

```yaml
qubes:
  infra:
    network: none        # or leave infra out of qubes: to keep its netvm as it is
services:
  chat: { qube: infra, tags: [tag:chat], ports: [{port: 443, target: 8008, tls: yes}] }
```

This needs HTTPS certificates on the tailnet
([`tailscale.md`, "HTTPS"](tailscale.md#https)). `chat` is its own device on
the tailnet; connections reach `infra`'s port 8008 over qrexec, whatever
`infra`'s network is. A qube that only hosts services and isn't listed under
`qubes:` keeps its netvm, and any Tailscale it runs itself. A web app that checks its own
origin (a configured base URL or public address) must be told its new address,
`https://chat.<your-tailnet>.ts.net`.

**Internet through exit nodes:** define an egress once at the top of the file,
`egress: {vpn: {exits: [<exit node>, <another>]}}`, and give a qube `egress: vpn`
(with `network: proxy` for transparent routing). The qube needs no tailnet
identity. The exit nodes and the egress's grant are in
[`tailscale.md`, "Exit nodes"](tailscale.md#exit-nodes); how it fails closed,
in [`walkthrough-egress.md`](walkthrough-egress.md).

## 10. Adopt it a step at a time

Each step changes only what it names, and can be undone on its own:

1. **Nothing yet.** A file with only `machine:` plans no changes; no proxy
   qube is created until something uses one.
2. **One qube through exit nodes.** Add an egress and one qube:
   ```yaml
   machine: m1
   egress:
     vpn: { exits: [exit1, exit2] }
   qubes:
     browser: { network: proxy, egress: vpn, outbound: { internet: all } }
   ```
   `apply` creates `sys-torii` and one identity, `m1-egress-vpn`; sign it in
   (`torii show` prints the link) and grant it the internet
   ([`tailscale.md`](tailscale.md#exit-nodes)). Only `browser` changes: its
   netvm becomes `sys-torii`, and its old netvm is recorded. Nothing is
   installed in it or its template.
3. **More qubes**, one entry each. Others stay as they are.
4. **A tailnet identity** for a qube that needs the tailnet:
   `tailnet: {identity: tagged}`. Its tailnet traffic uses it; its internet
   stays on the egress.
5. **Qubes with no network card** (`network: none` with `outbound:`) need the
   client in their template; `plan` says which.

To undo a step, take its lines out and `apply`. A qube leaving the file gets
no netvm, and `plan` prints the command that puts back the one it had
([SW-261127-VPGK](../claims/apply.md#sw-261127-vpgk-a-qube-adopted-records-its-netvm-once-a-qube-leaving-the-file-is-left-with-no-netvm)).

## Disposables through an egress

A disposable gets its netvm from its disposable template, not from the qube
that opens it, and can't be listed in the file. So give the disposables a
disposable template of their own behind a proxy whose `unlisted:` sends
them through the egress, and keep `sys-torii` refusing qubes nobody listed:

1. Create the disposable template (here from `fedora-43-xfce`):
   ```sh
   qvm-create --class AppVM --template fedora-43-xfce --label red browser-dvm
   qvm-prefs browser-dvm template_for_dispvms True
   ```
2. In the file:
   ```yaml
   machine: m1
   egress:
     vpn: { exits: [exit1, exit2] }
   proxies:
     - { name: sys-torii-dvm, unlisted: { internet: all, egress: vpn } }
   qubes:
     browser-dvm:            # its disposables use sys-torii-dvm
       network: proxy
       proxy: sys-torii-dvm
       disposables: yes      # listing a disposable template changes every disposable made from it
       egress: vpn
       outbound: { internet: all }
   ```
   `sys-torii-dvm` carries its own `vpn` identity, `m1-egress-vpn-sys-torii-dvm`;
   sign it in too.
3. Make the qubes that open links in disposables use it:
   `qvm-prefs browser default_dispvm browser-dvm`. torii doesn't set this.
4. `torii check` warns about any qube on an egress whose `default_dispvm`
   would still go out another way, and `torii show` lists the disposables
   it sees on `sys-torii-dvm`, by name.

## Everything through torii by default

Qubes' global default netvm decides where new qubes go. Point it at a torii
proxy and choose what that means with the proxy's `unlisted:`:

- `unlisted: refuse` (the default): **new qubes are offline** until you list
  them ([SW-261127-PXX9](../claims/connections.md#sw-261127-pxx9-the-proxy-must-decide-a-connection-against-the-entry-of-the-qube-it-came-from-and-only-by-a-way-that-entry-offers)).
- `unlisted: { internet: all, egress: vpn }`: **new qubes reach the internet
  only through the exit nodes**, failing closed.

torii never changes the global default; set it yourself
(`qubes-prefs default_netvm sys-torii`) once `sys-torii` exists. Qubes'
own service qubes keep their netvms.

## Day to day

- **Change something:** edit `~/torii.yaml`, then `torii plan` and `torii apply`.
  The proxy loads its new config in place; no qube restarts, but open
  connections through the proxy drop once.
- **Upgrade:** install the new RPM as in step 2, then `torii apply`. It
  updates the proxy template and restarts `sys-torii`, so networked qubes lose
  their network for under a minute and tailnet connections drop once.
- **Check now and then:** `torii check`. It also reports hand-written policy
  that opens more than the file says.
- **Remove a qube:** delete its entry and `torii apply`. It goes back to the
  file's defaults (no network card, with the defaults above). Remove its device in
  the Tailscale admin console yourself.
- **Logs:** `torii log QUBE`. In `sys-torii`: `journalctl -u torii-proxy`.

## Undo everything

1. Empty `qubes:` in `~/torii.yaml` and `torii apply`. Every managed qube is
   left with no netvm; the plan names the netvm each had before torii and
   the `qvm-prefs` command that puts it back.
2. `qvm-shutdown --wait sys-torii && qvm-remove -f sys-torii torii-fedora-44-minimal`.
3. `sudo rm /etc/qubes/policy.d/50-torii.policy && sudo dnf remove torii-dom0`.
4. Remove the devices in the Tailscale admin console.

## Commands

All run in dom0. `-f FILE` picks the machine file (default `~/torii.yaml`).

| Command | What it does |
|---|---|
| `torii plan [--diff]` | Every change `apply` would make, in order; `--diff` shows the policy file's diff |
| `torii apply [-y]` | Makes those changes, after asking (`-y`: without asking). Stops at the first failing step; run it again after fixing the problem, and it picks up where it stopped |
| `torii show [QUBE] [--json]` | What each managed qube can reach and what can reach it, read from the live system |
| `torii check` | Where the live system and the file disagree |
| `torii log QUBE` | Its recent connection decisions: allowed, refused, and why |
| `torii log --egress NAME` | An egress's events: exit-node switches, nodes going down and coming back |
| `torii grants` | Proposed `tagOwners` and grants for the tailnet policy |
| `torii sign-in-key NAME < key` | Signs an identity in with a one-time auth key, which is stored only in the proxy qube. NAME is a qube, a service, or `egress:<name>` (`egress:<name>@<proxy>` when several proxies carry it) |
| `torii install-client TEMPLATE` | Installs the client package that qubes with no network card need for outbound access |
| `torii snapshot` | Prints the live state that `plan` reads (for bug reports and tests) |

`show` reads the live system, not the file: prefs, every policy file (hand-
written ones too), the config each proxy holds, and the identities' state.
`check` reports drift from the file, a proxy that's down or runs an older
config, identities not signed in, and hand-written policy that widens what
the tool set.

torii only changes the qubes listed under `qubes:`. The first `apply` that
lists a qube records the netvm it had (features `torii-prior-netvm` and
`torii-adopted`, written once). A qube removed from the file gets no netvm at
the next `apply`, failing closed; `plan` names the netvm it had before torii
and the command to restore it, but never restores it itself. A qube that only
hosts services, and isn't listed under `qubes:`, is never touched. Tailnet
devices stay in the admin console until you remove them.

## The machine file

| Key | Default | Meaning |
|---|---|---|
| `machine` | required | This machine's name. It prefixes tags (`server` becomes `tag:m1-server`) and device names (`wiki` becomes `m1-wiki`); every tagged device also carries `tag:m1`. |
| `mesh` | | A name for a file applied on its own (`torii -f ~/test.yaml apply`). Files with different `mesh:` values never change each other's qubes. |
| `proxy` | `sys-torii` | The proxy qube: a name, or `{name, netvm, label, unlisted}` (netvm `sys-firewall`). |
| `proxy.unlisted`, `proxies[].unlisted` | `refuse` | Qubes attached to that proxy but not listed under `qubes:`, disposables included: `refuse`, or `{internet, egress}` (`internet` as in `outbound.internet`). Never a tailnet identity. |
| `egress.<name>` | | An egress: an ordered list of Tailscale exit nodes for internet traffic, failing closed. `exits` (in order, required), `failback-after` (`2m`), `tags` (`[egress]`), `hostname` (`<machine>-egress-<name>`; `-<proxy>` is added on other proxies), `probe` (`host:port`, none). See [`tailscale.md`](tailscale.md#exit-nodes). |
| `proxies` | | More proxy qubes, chosen per qube with `proxy: <name>`. A proxy qube is created once something uses it; the main proxy is never removed by `apply`, since it holds identities. |
| `template`, `base-template` | `torii-<base-template>`, `fedora-44-minimal` | The tool's own template and what it's cloned from. A new base is a new template: `apply` creates it, moves the proxies onto it (one restart each) and removes the old one once nothing uses it. |
| `tailnet.control-url` | Tailscale | A Headscale (or other) coordination server. |
| `tailnet.tag-owners` | `[autogroup:admin]` | For `torii grants`. |
| `tailnet.tailscale-logs` | `no` | `yes` lets each identity upload Tailscale's client logs to `log.tailscale.com`, which Tailscale support asks for. They go over the proxy qube's own network, not through an egress. Off, the control server is told so (no logs, no support). tsnet's port mapper (UPnP, NAT-PMP, PCP) is always off: behind Qubes' NAT it only broadcasts ([SW-261127-8PTP](../claims/identity.md#sw-261127-8ptp-the-proxy-must-not-send-tailscale-client-logs-unless-the-file-asks-and-must-not-map-ports)). |
| `status-readers` | | Admin qubes that may read `torii.Status` besides dom0. |
| `defaults` | | Any per-qube key below except `forwards` and `local-inbound`. |

Per qube:

| Key | Values | |
|---|---|---|
| `network` | `none` \| `proxy` \| `default` \| a netvm | **Required**, in the entry or in `defaults`. Where its network comes from. `none`: no netvm, qrexec only. `proxy`: netvm is its proxy qube. `default`: Qubes' default netvm. A netvm's name: that netvm. Only `none` and `proxy` can have a tailnet identity, `outbound:` and forwards. |
| `proxy` | a proxy qube | Which proxy handles it: its netvm when routed, its qrexec target otherwise, and where its identity lives. Default: the main `proxy:`. |
| `outbound.internet` | `none` \| `all` \| a list | Public addresses. List entries: `name`, `*.domain`, address or range, each with an optional `:port` and an optional `/tcp` or `/udp`. **An entry without a protocol is TCP only;** UDP (QUIC, DNS, VPNs) needs `/udp`: `example.com:443/udp`. A raw address nobody resolved and no entry covers is refused. The same entries work in `outbound.lan` and `outbound.tailnet`. |
| `outbound.lan` | `none` \| `all` \| a list | This machine's private network (`192.168/16`, `172.16/12`, `10/8` outside Qubes', `fc00::/7`), never through an exit node. Default `none`: `internet: all` does not include it. |
| `outbound.tailnet` | `yes` \| `no` | May open connections to the tailnet; which destinations it reaches there is decided by the tailnet's grants (the list form narrows it further). |
| `egress` | `direct` \| an egress | Its internet and DNS through that egress's exit nodes, failing closed. Needs `outbound.internet`. LAN traffic still goes direct. |
| `tailnet.identity` | `none` \| `tagged` \| `login` | Tagged (owned by its tags), or signed in as a person. |
| `adopt` | `force` | Lets the first `apply` move it off a netvm torii doesn't know (not none, Qubes' default, or a torii proxy), such as `sys-whonix` or a VPN qube. Without it, `plan` refuses. |
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

## What torii owns

<!-- charter:embed SW-261127-G26T -->
> **[SW-261127-G26T](../claims/apply.md#sw-261127-g26t-apply-changes-only-what-torii-owns-its-template-its-proxy-qubes-its-policy-files-and-named-workload-settings) apply changes only what torii owns: its template, its proxy qubes, its policy files, and named workload settings**
>
> torii creates, changes and removes only its own template, the proxy
> qubes it created (tagged `torii-owned`, and with the mesh's tag for a
> named mesh), its mesh's policy file `50-torii` (or `50-torii-<mesh>`) and
> the shared deny file `59-torii-deny`; it may also download the base
> template its own is cloned from, which it never changes. On a qube the
> file lists it sets only the netvm, the `torii-client` service and its
> feature, the `torii-managed` tag, and the adoption features written
> once, and removes the earlier `torii-netvm-kept` tag. Restarting a proxy
> detaches every running qube behind it and attaches it again, listed or
> not. It never creates or removes a workload, and a qube that only hosts
> a service is not changed.
<!-- /charter:embed -->

- **Reads, never writes:** every other policy file. Grants to
  `qubes.ConnectTCP` or `torii.*` written by hand show up in `show` and
  `check`.
- **The proxy's config** goes to it as the features `vm-config.torii*`
  (compressed and split, since QubesDB caps a value under 3 KB), and is
  taken only whole ([SW-261127-XGDY](../claims/apply.md#sw-261127-xgdy-a-proxy-takes-its-config-whole-or-not-at-all-and-a-failed-check-never-replaces-the-running-one)). The proxy reloads when they change.

## Troubleshooting

- **In the proxy qube:** `journalctl -u torii-proxy`, and
  `/usr/lib/torii/torii-proxy status|log|explain -workload Q -dest host:port`.
- **A networked qube can't resolve a tailnet short name** such as `peer`.
  Use the full name (`peer.tailXXXX.ts.net`). Fedora's `systemd-resolved`
  doesn't send single-label names to the DNS server, and Qubes can't push a
  search domain.
- **"connected" then closed, for a networked qube:** connections are
  intercepted, so the connection opens first and closes when the proxy
  refuses or can't reach the destination. `torii log QUBE` says which.
- **The proxy qube can't shut down:** a netvm with running clients won't.
  `apply` detaches and reattaches them when it must restart the proxy.
