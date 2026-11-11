# torii

> **Experimental.** torii is under active development. The machine file,
> the commands and the names it creates on your machine may change between
> versions without a migration path. It changes dom0 policy and your qubes'
> networking, so try it on a machine you can afford to rebuild, not one you
> depend on.
>
> torii is an independent project. It is not part of, or endorsed by, the
> Qubes OS Project.

Give each qube in Qubes OS its own Tailscale identity and its own rules for
what it can reach and what can reach it. That includes qubes with no network
card at all.

You describe every qube's connectivity in one YAML file in dom0.
`torii apply` makes the machine match it, and `torii show` reads back from
the live system what each qube can really reach.

```yaml
machine: m1
defaults:
  network: none                      # no network card unless a qube says otherwise
  tailnet: { tags: [server] }
egress:
  vpn: { exits: [exit1, exit2] }     # exit nodes in order; none healthy: refused, never direct
qubes:
  wiki:                              # offline, but reachable from the tailnet on 443
    tailnet: { identity: tagged, inbound: [{port: 443, target: 8080}] }
  build:                             # offline; may fetch from PyPI and use the tailnet
    outbound: { internet: [pypi.org, files.pythonhosted.org], tailnet: yes }
    tailnet: { identity: tagged, tags: [job] }
  browser:                           # networked, its internet through vpn; no identity needed
    network: proxy
    egress: vpn
    outbound: { internet: all }
```

## Why

Tailscale connects machines, not compartments. Run it in a network qube and
every qube behind it is one device with one address and one set of grants. A
laptop allowed to reach one service there reaches all of them, and a
compromised qube can reach whatever the machine can. A qube with no network
card can't run a Tailscale client at all, so it can't take part unless you
hand-build a forward through another qube.

Doing it by hand means netvms, qrexec policy, services and state inside
templates, and the tailnet policy, all kept in step. Afterwards nothing tells
you what a qube can actually reach.

With torii:

- **Every qube gets its own identity.** Each qube that needs one is its own
  tailnet device with its own tags, so tailnet grants apply per qube.
- **Offline qubes can use the tailnet.** A qube with no netvm can serve ports
  to the tailnet and reach the destinations you allow. Each connection is a
  qrexec call that dom0 allows for that port only. The qube never gets a
  network card.
- **Internet access is allowlisted by name.** List names like `pypi.org` or
  `*.example.com:443`. The proxy checks them against the name the qube looked
  up and the TLS server name it sends. Or send a qube's internet through
  exit nodes, tried in order, without giving it an identity of its own. If
  none is up, connections fail rather than going direct.
- **You can see the result.** `torii show` reads netvms, every policy file,
  the proxy's config and each identity's state. `torii check` reports where
  they differ from the file. `torii log` shows why each connection was
  allowed or refused.
- **No credentials in dom0.** A new identity joins through a sign-in link
  that the tag's owner approves. Auth keys are optional, and are stored only
  in the proxy qube.

## How it works

```
        tailnet (Tailscale or Headscale)
                    │  one identity per qube
          ┌─────────┴─────────┐
 dom0     │  sys-torii        │── sys-firewall ── internet
 torii ──►│  torii-proxy      │
          └───┬───────────┬───┘
       qrexec │           │ netvm
        ┌─────┴────┐ ┌────┴─────┐
        │ wiki     │ │ browser  │
        │ no netvm │ │ routed   │
        └──────────┘ └──────────┘
```

- **`torii`** is a small Python tool in dom0. It reads the file, plans the
  changes and makes them through the Qubes Admin API: netvms, one qrexec
  policy file, features, and the proxy qube and its template. It never
  handles network traffic.
- **`sys-torii`** is a proxy qube running `torii-proxy`, a Go program built
  on Tailscale's [`tsnet`](https://tailscale.com/kb/1244/tsnet). It holds one
  tailnet identity per qube. Kernel forwarding is off, so nothing passes
  unless the program relays it. It decides every connection against the file
  and logs it.
- **`torii-client`** runs in offline qubes that reach out. It offers an HTTP
  proxy, SOCKS and fixed forwards on loopback. Each connection becomes a
  `torii.Connect` qrexec call to the proxy qube.

[`docs/design.md`](docs/design.md) explains why it is built this way.

## What it can do

| Pattern | In the file | What happens |
|---|---|---|
| An **offline service** reachable from the tailnet | `tailnet: {identity: tagged, inbound: [443]}` | The qube keeps no netvm. Its identity listens on 443, and each connection is a `qubes.ConnectTCP` call into the qube, allowed by dom0 on that port only. |
| An **offline job** that fetches packages and uses the tailnet | `outbound: {internet: [pypi.org], tailnet: yes}` | Inside the qube, `HTTPS_PROXY` and SOCKS are on loopback. The proxy allows only the names on the list, and tailnet destinations the grants allow. |
| A **networked qube** behind the proxy | `network: proxy` | Its netvm becomes `sys-torii`. All its traffic and DNS are intercepted and decided per destination, and tailnet traffic goes out on its own identity. |
| Internet **through exit nodes** | `egress: vpn`, with `egress: {vpn: {exits: [a, b]}}` | Its internet and DNS go through the first healthy exit node, failing over in order and back after a hold-down. With none healthy, connections are refused, never sent direct. The egress has one internet-only identity, so the qube needs none. |
| **Qubes attached by hand** | `proxy: {unlisted: refuse}` (default) or `{internet: all, egress: vpn}` | A qube attached to a proxy but not in the file gets nothing, or the internet through an egress. Disposables are such qubes: their disposable template's proxy decides. |
| **Named services** | `services: {chat: {qube, tags, ports}}` | Each service is its own tailnet device. With `tls: yes` it serves HTTPS with a Tailscale certificate. |
| **Local access** between qubes | `local-inbound: [{port: 5432, from: [job]}]` | `job` reaches the qube's port 5432 on `127.0.0.1:5432` over qrexec, and nothing else. |

## Quick start

You need a Qubes 4.3 machine, a qube with network and Go 1.21+ to build in,
and admin rights on a tailnet. [`docs/setup.md`](docs/setup.md) walks through
the same steps with the output to expect at each one.

**1. Build** in a qube (here `dev`). This produces one RPM that carries
everything:

```sh
git clone https://github.com/leroux/torii-qubes.git && cd torii-qubes
make          # build/torii-dom0-<version>.noarch.rpm
```

**2. Install** it in dom0:

```sh
qvm-run --pass-io dev 'cat ~/torii-qubes/build/torii-dom0-*.noarch.rpm' > /tmp/torii.rpm
sudo dnf install /tmp/torii.rpm
```

**3. Write `~/torii.yaml`** in dom0. Start from the example above or from
[`examples/machine.yaml`](examples/machine.yaml). The qubes must already
exist. The tool changes only their network settings, and never creates or
deletes your qubes.

**4. Add the tags** to your tailnet policy's `tagOwners`, with you as an owner
([`docs/tailscale.md`](docs/tailscale.md#tags)).

**5. Plan and apply:**

```sh
torii plan     # every change, in order
torii apply    # the first run downloads a template and creates sys-torii
```

**6. Sign identities in.** `torii show` prints a sign-in link for each new
identity. Open it as an owner of the identity's tags and approve it.

**7. Grant access.** `torii grants` prints proposed grants. Narrow them,
then paste them into the tailnet policy. A port the file offers but no grant
allows is never reached.

**8. Check:** `torii show`, `torii check`, `torii log QUBE`.

Offline qubes that reach out also need the client package in their template
(`torii install-client <template>`); `plan` tells you which templates lack it.

## Advanced

Every key below is optional except `machine` and `network`. Full details are in
[`docs/reference.md`](docs/reference.md).

### Machine name and control server

```yaml
machine: m1          # required; prefixes tags (tag:m1-server) and device names (m1-wiki)
mesh: test           # a separate file, applied on its own: torii -f ~/test.yaml apply
tailnet:
  control-url: https://headscale.example.org   # default: Tailscale
  tag-owners: [autogroup:admin]                # used by `torii grants`
  tailscale-logs: no                           # yes: tsnet uploads client logs to Tailscale
```

Files with different `mesh:` values never change each other's qubes.

### Netvm per qube

`network:` is required for every qube, in its entry or in `defaults`.

| Value | Netvm |
|---|---|
| `none` | none; tailnet and internet only over qrexec, as the file allows |
| `proxy` | the qube's proxy qube, which decides every connection and DNS query |
| `default` | Qubes' default netvm |
| a qube name | that netvm |

torii changes only the qubes listed under `qubes:`. The first `apply` records each
qube's previous netvm. `adopt: force` is required to move a qube off a netvm
torii doesn't recognize, such as `sys-whonix` or a VPN qube. `disposables: yes`
is required to list a disposable template. A qube removed from the file gets
netvm none, and `plan` prints the command that restores its old netvm.

### Outbound allowlists

```yaml
  build:                    # a qube, under qubes:
    network: none
    outbound:
      internet: [pypi.org, "*.github.com:443", "example.com:443/udp", 203.0.113.0/24]   # or all, or none
      lan: [192.168.1.10:631]   # private addresses; not included in internet: all
      tailnet: yes              # tailnet destinations; the tailnet's grants decide which
    tailnet: { identity: tagged }   # outbound.tailnet needs an identity
```

An entry is a name, `*.domain`, an address or a range, with an optional `:port`.
Entries are TCP unless they end in `/udp`.

### Exit nodes

```yaml
egress:
  vpn:
    exits: [exit-home, exit-cloud]   # tried in order
    failback-after: 2m               # how long a higher node must stay healthy before torii switches back
    tags: [egress]                   # tags of the egress identity
    hostname: m1-egress-vpn          # its device name
    probe: example.com:443           # optional check through the active exit node
qubes:
  browser: { network: proxy, egress: vpn, outbound: { internet: all } }
```

The qube's internet traffic and DNS go through the first healthy exit node. All
qubes on the egress share one identity, which may only reach the internet. When
no exit node is healthy, connections are refused and DNS fails. Nothing goes out
directly. LAN traffic doesn't go through the exit node. `torii log --egress vpn`
shows switches between exit nodes.

### Proxy qubes

```yaml
egress: { vpn: { exits: [exit-home] } }
proxy: { name: sys-torii, netvm: sys-firewall, label: orange, unlisted: refuse }
proxies:
  - { name: sys-torii-dvm, unlisted: { internet: all, egress: vpn } }
  - { name: sys-torii-vpn, netvm: sys-vpn }
template: torii-fedora-44-minimal    # the proxies' template
base-template: fedora-44-minimal     # what that template is cloned from
status-readers: [admin-qube]         # qubes other than dom0 that may read status
```

A qube uses another proxy with `proxy: <name>`. A proxy qube is created the first
time something uses it. `unlisted:` sets what qubes attached to that proxy, but
not listed in the file, may reach: `refuse` (the default), or `internet` and
`egress` as for a qube. A disposable takes its netvm from its disposable
template, so put that template on a proxy such as `sys-torii-dvm` to decide what
its disposables get.

### Tailnet identity and inbound ports

```yaml
  wiki:                      # a qube, under qubes:
    network: none
    tailnet:
      identity: tagged       # none | tagged | login (signed in as a person)
      tags: [server]         # becomes tag:<machine>-server
      hostname: wiki         # default: the qube's name
      inbound: [22, { port: 443, target: 8080, tls: yes }]   # tls: HTTPS with a Tailscale certificate
```

`torii show` prints a sign-in link for each new identity. To use an auth key
instead: `torii sign-in-key QUBE < key`. The key is stored only in the proxy qube.

### Services

```yaml
services:
  chat:
    qube: infra
    tags: [tag:chat]     # full tags are allowed here, for tags shared between machines
    hostname: chat
    proxy: sys-torii
    ports: [{ port: 443, target: 8008, tls: yes }]
```

Each service is its own tailnet device. torii leaves the hosting qube's netvm
alone unless the qube is also listed under `qubes:`.

### Offline qubes: forwards, SOCKS, HTTP proxy, local ports

```yaml
  job:                       # a qube, under qubes:
    network: none
    outbound: { internet: [pypi.org], tailnet: yes }
    tailnet: { identity: tagged }
    forwards: { db: { to: "store:5432", port: 5432 } }   # listens on 127.0.0.1:5432 in the qube
    socks: 1080                                          # SOCKS port, or no
    http-proxy: 3128                                     # HTTP proxy port, or no
    local-inbound: [{ port: 8000, from: [build] }]       # qubes on this machine allowed to reach port 8000
```

`forwards`, `socks` and `http-proxy` need the client package in the qube's
template: `torii install-client TEMPLATE`.

### Defaults

`defaults:` accepts every per-qube key except `forwards` and `local-inbound`.
A qube's own entry overrides it.

### Commands

| Command | |
|---|---|
| `torii plan [--diff]` | every change `apply` would make, in order |
| `torii apply [-y]` | make the changes; after a failure, run it again to continue |
| `torii show [QUBE] [--json]` | what each qube can reach, read from the live system |
| `torii check` | where the live system differs from the file |
| `torii log QUBE` | recent connections and why each was allowed or refused |
| `torii log --egress NAME` | exit node switches and health changes |
| `torii grants` | proposed tag owners and grants for the tailnet policy |
| `torii sign-in-key NAME < key` | sign in a qube, a service or `egress:NAME` with an auth key |
| `torii install-client TEMPLATE` | install the client package in a template |
| `torii snapshot` | the live state `plan` reads, for bug reports |

Every command takes `-f FILE` (default `~/torii.yaml`). For many machines, see
[`docs/salt.md`](docs/salt.md).

## Documentation

| | |
|---|---|
| [`docs/setup.md`](docs/setup.md) | Setting up a machine step by step, then each pattern |
| [`docs/tailscale.md`](docs/tailscale.md) | The tailnet side: tags, grants, HTTPS, sign-in, exit nodes, Headscale |
| [`docs/reference.md`](docs/reference.md) | Every command and machine-file key, what the tool owns, troubleshooting, limits |
| [`docs/design.md`](docs/design.md) | The problem, the model, and why it is built this way |
| [`docs/testing.md`](docs/testing.md) · [`docs/performance.md`](docs/performance.md) | How it is tested, and how fast it is |
| [`docs/salt.md`](docs/salt.md) | Configuring many machines from Salt |
| [`HACKING.md`](HACKING.md) · [`proxy/README.md`](proxy/README.md) | Working on the code |

## Status

Experimental (see the note at the top). Tested end to end on Qubes 4.3
with a Fedora 44 proxy template and Fedora 43 qubes, against Tailscale.
Qubes 4.2 should work but hasn't been tested. Headscale is tested by the
automated tests but not yet on a Qubes machine. `make test` simulates every
connection a machine file implies through the real proxy engine
([`docs/testing.md`](docs/testing.md)). Known limits are listed in
[`docs/reference.md`](docs/reference.md#limits).

## License

MIT, see [`LICENSE`](LICENSE).
