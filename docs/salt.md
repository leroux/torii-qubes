# Using lintel from Salt

For configuring lintel on many Qubes machines from Salt. Salt keeps
each machine's file and the tool's version; the tool does the rest:
`lintel apply` works out what differs and changes only that, like a state
run. No Salt module is needed.

Salt in Qubes runs in dom0 with `qubesctl`, on each machine by itself (no
master). User states go in `/srv/user_salt`, and your states get there
the way anything reaches dom0: from a qube, by `qvm-run --pass-io`.

## The states

```yaml
# not a machine file: /srv/user_salt/lintel/init.sls
lintel-package:
  cmd.run:   # dom0 has no repositories: install the RPM shipped with the states
    - name: dnf install -y --disablerepo='*' /srv/user_salt/lintel/lintel-dom0-{{ pillar.lintel.version }}-1.noarch.rpm
    - unless: rpm -q lintel-dom0-{{ pillar.lintel.version }}

/etc/lintel/machine.yaml:
  file.serialize:   # this machine's file, from its pillar
    - dataset_pillar: lintel:machine
    - serializer: yaml
    - makedirs: true
    - mode: '0644'

lintel-apply:
  cmd.run:
    - name: lintel -f /etc/lintel/machine.yaml apply --yes
    # Runs when the file changed or the machine drifted from it (a netvm
    # changed by hand): `plan` compares the machine, not just the file.
    - unless: lintel -f /etc/lintel/machine.yaml plan | grep -q '^No changes'
    - require:
      - cmd: lintel-package
      - file: /etc/lintel/machine.yaml
```

The first `apply` on a machine downloads the base template and builds the
tool's own; it takes minutes. Later ones take seconds.

## The pillar

Each machine's pillar holds its machine file under `lintel:machine`, exactly
as in [`setup.md`](setup.md) and [`reference.md`](reference.md):

```yaml
# not a machine file: pillar/laptops/alice.sls
lintel:
  version: 0.1.0.58
  machine:
    machine: alice-laptop
    defaults:
      tailnet: { identity: tagged, tags: [work] }
    qubes:
      work:     { network: proxy, outbound: { internet: all, tailnet: yes } }
      build:    { outbound: { internet: [pypi.org, files.pythonhosted.org] } }
```

Shared parts (`defaults`, `tailnet`, `tag-owners`) can come from a common
pillar merged in, or a Jinja import. Several meshes on one machine: one file
and one `lintel-apply` state each (`mesh:` in each file).

## Signing identities in without a person

A new identity waits for someone to open its sign-in link. On a fleet, give
it a tagged auth key instead (Tailscale: a pre-approved, tagged key; Headscale:
`headscale preauthkeys create`). `lintel sign-in-key` reads it from stdin. It
throws away the identity's state and signs in again, a new device each time,
so run it only while the identity is waiting:

```yaml
# not a machine file
{% for q in pillar.lintel.get('sign_in', []) %}
lintel-sign-in-{{ q }}:
  cmd.run:
    - name: lintel -f /etc/lintel/machine.yaml sign-in-key {{ q }}
    - stdin: {{ pillar.lintel.authkey }}
    - onlyif: lintel -f /etc/lintel/machine.yaml show --json | python3 -c "import json,sys; sys.exit(0 if any(i['hostname']=='{{ q }}' and i['state']=='NeedsLogin' for v in json.load(sys.stdin)['qubes'] for i in v.get('identities',[])) else 1)"
    - require:
      - cmd: lintel-apply
{% endfor %}
```

`sign_in` lists identities by their tailnet name (a service's name, or the
qube's device name, `<machine>-<qube>` by default). Keep `authkey` in an
encrypted pillar; the tool stores it only in the proxy qube.

## What to keep out of other states

On the qubes in a machine's file, lintel owns `netvm`, the `ipv6` feature and
the `lintel-*` tags and features; on its own qubes (the proxy qubes and its
template), everything. Another state that sets these
would undo `apply`, and `apply` would undo it. `lintel check` lists such
drift.

## Not there yet

- `lintel plan` exits 0 whether or not there are changes; the `unless` above
  reads its output instead. An exit code for "changes pending" would be
  cleaner.
- In `test=True`, Salt shows only that `lintel-apply` would run, not the plan.
  Run `lintel -f /etc/lintel/machine.yaml plan` to see it.
