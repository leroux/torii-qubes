# Setting up a machine

From nothing to one service, in a qube with no network card, reachable from your tailnet, then the
other patterns, one at a time. Plan on an hour the first time; most of it is
the first `apply` (downloading and preparing a template).

The tailnet side (tags, grants, HTTPS, sign-in) is in
[`tailscale.md`](tailscale.md); this guide links there at each step that
needs it. Every command and key is in [`reference.md`](reference.md).

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
git clone https://github.com/Gaia-PBC/torii-qubes.git && cd torii-qubes
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
8080 in the qube.

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
again. It starts where it stopped.

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
A few seconds later `torii show` says `Running 100.x.y.z`.

Then let people in. `torii grants` prints a proposal:

```json
{
  "tagOwners": { "tag:server": ["autogroup:admin"] },
  "grants": [ { "src": ["autogroup:member"], "dst": ["tag:server"], "ip": ["tcp:80"] } ]
}
```

Narrow `src` if not every member should reach it, and paste it into the
tailnet policy ([`tailscale.md`, "Grants"](tailscale.md#grants)).

## 8. Check it

From a laptop on the tailnet, open `http://wiki` (or
`http://wiki.<your-tailnet>.ts.net`). It should show the service.

On the machine:

```sh
torii check     # OK: the live system matches /home/user/torii.yaml
torii log wiki  # one line per connection: who, allowed or refused, why
torii plan      # No changes: the machine matches /home/user/torii.yaml
```

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
`curl -x http://127.0.0.1:3128 https://wikipedia.org` is refused.

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
[`tailscale.md`, "Exit nodes"](tailscale.md#exit-nodes).

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
no netvm, and `plan` prints the command that puts back the one it had.

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
  them.
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
