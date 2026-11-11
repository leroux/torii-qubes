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
git clone https://github.com/Gaia-PBC/lintel-qubes.git && cd lintel-qubes
make
```

The first `make` fetches the Go toolchain the code needs and the packaging
tool, nfpm, at pinned versions, so it takes a minute or two. Clone in full
(not `--depth 1`): the package version counts commits, and a shallow clone
builds one that looks older than what you have installed.

Expect it to end with:

```
created package: ../../build/lintel-dom0-0.1.0.N-1.noarch.rpm
```

`make test` runs the unit tests if you want to check the build (it needs
`python3-pyyaml`, or `python3-yaml` on Debian). The RPM is the only file a machine needs, so you can also build it once
and copy it to other machines.

## 2. Install it in dom0

In dom0 (replace `dev` with your build qube, and `~/lintel-qubes` with the
checkout if you cloned elsewhere):

```sh
qvm-run --pass-io dev 'cat ~/lintel-qubes/build/lintel-dom0-*.noarch.rpm' > /tmp/lintel-dom0.rpm
sudo dnf install /tmp/lintel-dom0.rpm
lintel --version
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

In dom0, `~/lintel.yaml`:

```yaml
machine: m1
proxy: sys-lintel
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
lintel plan
```

On a machine that has never run lintel, expect this (numbers differ):

```
fedora-44-minimal: download the base template (qvm-template install)
lintel-fedora-44-minimal: create the proxy template, a clone of fedora-44-minimal
lintel-fedora-44-minimal: install the proxy package 0.1.0.N
sys-lintel: create the proxy qube (template lintel-fedora-44-minimal, netvm sys-firewall, provides network)
sys-lintel: set netvm = sys-firewall
sys-lintel: set provides_network = True
sys-lintel: set autostart = True
sys-lintel: enable service lintel-proxy
sys-lintel: set its config 1a2b3c4d5e6f: 1 workload
sys-lintel: start it and wait until it runs config 1a2b3c4d5e6f
50-lintel: write the qrexec policy
wiki: set netvm = (none) (was sys-firewall)
wiki: tag lintel-managed
wiki: set feature lintel-prior-netvm = sys-firewall
wiki: set feature lintel-adopted = 2026-11-10

15 changes. Run `lintel apply` to make them.
```

`lintel plan --diff` also shows the policy file it would write. If `plan`
refuses with `cannot apply ... to this machine`, the message says why: a qube
that isn't there, a netvm named as a workload, a `sys-lintel` you made
yourself, and so on. Two refusals protect setups lintel can't judge: a qube
on a netvm it doesn't know (`sys-whonix`, a VPN qube) needs `adopt: force`
before lintel moves it, and a disposable template needs `disposables: yes`.

## 6. Apply

```sh
lintel apply
```

It lists the changes, asks, then numbers each step as it goes:
`[1/13] fedora-44-minimal: download the base template ...`. The first three
steps take most of the time: downloading the template (a few hundred MB),
cloning it, and installing the proxy's packages into it through the updates
proxy. After that it creates and starts `sys-lintel` and waits until the
proxy program reports the new config. It ends with `Done.`.

If a step fails, `apply` stops there:

```
apply stopped: lintel-fedora-44-minimal: install the proxy package 0.1.0.N: installing the proxy's dependencies failed (exit 1): ...
Fix it and run `lintel apply` again; finished steps are kept.
```

Fix the cause (here: the template's updates proxy) and run `lintel apply`
again. It starts where it stopped.

## 7. Sign the identity in, and grant access

```sh
lintel show
```

```
proxy sys-lintel: running, config 1a2b3c4d5e6f, 1 workloads
  waiting for sign-in: wiki: https://login.tailscale.com/a/0123456789abcd

wiki  (no network card, serves the tailnet)
  network card   no
  proxy qube     sys-lintel (qrexec)
  identity       m1-wiki (tagged tag:m1-server,tag:m1) NeedsLogin
  tailnet in     80->8080 (who: the tailnet's grants)
  tailnet out    no
  internet       none
```

Open the link in a browser where you're signed in to the tailnet as an owner
of `tag:m1-server` and `tag:m1`, and approve it. To skip the browser, use a one-time key
instead (`lintel sign-in-key wiki < key.txt`); both are in
[`tailscale.md`, "Signing identities in"](tailscale.md#signing-identities-in).
A few seconds later `lintel show` says `Running 100.x.y.z`.

Then let people in. `lintel grants` prints a proposal:

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
lintel check     # OK: the live system matches /home/user/lintel.yaml
lintel log wiki  # one line per connection: who, allowed or refused, why
lintel plan      # No changes: the machine matches /home/user/lintel.yaml
```

If the laptop can't connect:

- **Nothing in `lintel log wiki`:** the connection never reached the machine.
  Check the grant, and that the laptop and `wiki` are on the same tailnet.
- **`allow` in the log with an error:** the service in `wiki` isn't
  listening on 8080.
- **`waiting for sign-in` still in `lintel show`:** step 7 isn't done.

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
note: job-1 needs the lintel client in fedora-43-xfce; install it with `lintel install-client fedora-43-xfce`, then restart them
```

Run that, then `lintel apply`, then restart `job-1`. Inside it, programs that
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

Its netvm becomes `sys-lintel`. Inside it, reach tailnet devices by their
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

**Internet through an exit node:** `outbound: {via: <exit node name>}`. The exit
node and its grant are in [`tailscale.md`, "Exit nodes"](tailscale.md#exit-nodes).

## Day to day

- **Change something:** edit `~/lintel.yaml`, then `lintel plan` and `lintel apply`.
  The proxy loads its new config in place; no qube restarts, but open
  connections through the proxy drop once.
- **Upgrade:** install the new RPM as in step 2, then `lintel apply`. It
  updates the proxy template and restarts `sys-lintel`, so networked qubes lose
  their network for under a minute and tailnet connections drop once.
- **Check now and then:** `lintel check`. It also reports hand-written policy
  that opens more than the file says.
- **Remove a qube:** delete its entry and `lintel apply`. It goes back to the
  file's defaults (no network card, with the defaults above). Remove its device in
  the Tailscale admin console yourself.
- **Logs:** `lintel log QUBE`. In `sys-lintel`: `journalctl -u lintel-proxy`.

## Undo everything

1. Empty `qubes:` in `~/lintel.yaml` and `lintel apply`. Every managed qube is
   left with no netvm; the plan names the netvm each had before lintel and
   the `qvm-prefs` command that puts it back.
2. `qvm-shutdown --wait sys-lintel && qvm-remove -f sys-lintel lintel-fedora-44-minimal`.
3. `sudo rm /etc/qubes/policy.d/50-lintel.policy && sudo dnf remove lintel-dom0`.
4. Remove the devices in the Tailscale admin console.
