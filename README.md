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
git clone https://github.com/Gaia-PBC/torii-qubes.git && cd torii-qubes
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
