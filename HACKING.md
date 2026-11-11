# Hacking on torii

How to build, test and change torii on a Qubes machine. What
torii is and why: `docs/design.md`.
The proxy program in detail, with its test levels: `proxy/README.md`.

## Where things run

| Where | What | Needs |
|---|---|---|
| your **dev qube** (an AppVM) | the checkout, `go` (1.21+; `make` fetches the rest), `make`, unit tests, the Go integration levels | nothing from dom0 |
| **dom0** | the dom0 tool (`torii plan / apply / show ...`), its RPM, `qvm-template`, the qrexec policy files | a way in from the dev qube (below) |
| **test qubes** | proxy and workload qubes the tool creates; throwaway probes | created from dom0 or over the Admin API |

Most work never leaves the dev qube:

```sh
make test        # Go vet + unit tests, Python unit tests (qubesadmin mock); seconds
make test-all    # every level that can run here (proxy/README.md, "Tests")
make             # build/torii-dom0-*.noarch.rpm and build/bundle/*.{deb,rpm}
```

The dom0 tool's `plan` is golden-tested from recorded snapshots
(`dom0/tests/golden/`), so most changes to it can be made and checked
without touching dom0 at all. You need dom0 to record a new snapshot, to
check `apply` against a real machine, and to see the proxy and client
packages working in real qubes.

## Giving the dev qube access to dom0

Checking `apply` and the packages on a real machine needs a shell in dom0
from the dev qube. [`contrib/dom0-dev-access/`](contrib/dom0-dev-access/)
sets that up, and its README explains what it grants. **It gives the dev qube
full control of the machine**, so use a development machine. The commands
below assume `d0` from that directory is on your `PATH`.

With the Admin API part of that setup, the dom0 tool also runs from the dev
qube against the live machine, with no copying:

```sh
cd dom0 && python3 -m torii snapshot    # or show, plan -f ../examples/machine.yaml
```

## Day to day

### `dev/d0sync`: the working tree's dom0 tool, in dom0

```sh
make && dev/d0sync     # dom0/ and build/bundle/ -> dom0:~/torii-dev
d0 'cd ~/torii-dev/dom0 && TORII_BUNDLE=~/torii-dev/build/bundle python3 -m torii plan -f ~/torii.yaml'
```

dom0 pulls the files itself, with `qvm-run ... tar` into the dev qube, and
nothing is pushed to it. Code you sync runs in dom0 only when you run it.

### The packaged tool

```sh
make
d0 "qvm-run --pass-io $(qubesdb-read /name) 'cat $PWD/build/torii-dom0-*.noarch.rpm' </dev/null > /tmp/torii.rpm && sudo dnf install -y /tmp/torii.rpm"
d0 'torii --version'
```

### A new golden snapshot

`torii snapshot` prints the live state that `plan` reads. Record one from
the dev qube (`cd dom0 && python3 -m torii snapshot > /tmp/snap.json`),
then cut it down to the qubes the test needs. Keep personal qube names, the
`created-by-<your dev qube>` tags Qubes adds, and
anything else private out of `dom0/tests/golden/`.

### Test qubes

Name throwaway qubes `torii-probe*`, and hand-written probe policy
`40-torii-probe*.policy`, so they're easy to find and remove:

```sh
d0 'for q in $(qvm-ls --raw-list | grep "^torii-probe"); do qvm-kill $q 2>/dev/null; qvm-remove -f $q; done; sudo rm -f /etc/qubes/policy.d/40-torii-probe*.policy'
```

Policy the tool itself writes is `50-torii.policy`. Don't hand-edit it,
because `apply` replaces it.
