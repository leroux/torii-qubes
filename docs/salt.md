# Using torii from Salt

For configuring torii on many Qubes machines from Salt. Salt keeps
each machine's file and the tool's version; the tool does the rest:
`torii apply` works out what differs and changes only that, like a state
run. No Salt module is needed.

Salt in Qubes runs in dom0 with `qubesctl`, on each machine by itself (no
master). User states go in `/srv/user_salt`, and your states get there
the way anything reaches dom0: from a qube, by `qvm-run --pass-io`.

## The states

```yaml
# not a machine file: /srv/user_salt/torii/init.sls
torii-package:
  cmd.run:   # dom0 has no repositories: install the RPM shipped with the states
    - name: dnf install -y --disablerepo='*' /srv/user_salt/torii/torii-dom0-{{ pillar.torii.version }}-1.noarch.rpm
    - unless: rpm -q torii-dom0-{{ pillar.torii.version }}

/etc/torii/machine.yaml:
  file.serialize:   # this machine's file, from its pillar
    - dataset_pillar: torii:machine
    - serializer: yaml
    - makedirs: true
    - mode: '0644'

torii-apply:
  cmd.run:
    - name: torii -f /etc/torii/machine.yaml apply --yes
    # Runs when the file changed or the machine drifted from it (a netvm
    # changed by hand): `plan` compares the machine, not just the file.
    - unless: torii -f /etc/torii/machine.yaml plan | grep -q '^No changes'
    - require:
      - cmd: torii-package
      - file: /etc/torii/machine.yaml
```

The first `apply` on a machine downloads the base template and builds the
tool's own; it takes minutes. Later ones take seconds. A machine that
already matches its file plans nothing, so the state does nothing
([STK-261126-B4CP](../claims/needs.md#stk-261126-b4cp-torii-apply-must-change-only-what-torii-owns-and-must-make-exactly-the-steps-torii-plan-showed)).

## The pillar

Each machine's pillar holds its machine file under `torii:machine`, exactly
as in [`setup.md`](setup.md) and [its reference sections](setup.md#the-machine-file):

```yaml
# not a machine file: pillar/laptops/alice.sls
torii:
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
and one `torii-apply` state each (`mesh:` in each file).

## Signing identities in without a person

A new identity waits for someone to open its sign-in link. On a fleet, give
it a tagged auth key instead (Tailscale: a pre-approved, tagged key; Headscale:
`headscale preauthkeys create`). `torii sign-in-key` reads it from stdin. It
throws away the identity's state and signs in again, a new device each time,
so run it only while the identity is waiting:

```yaml
# not a machine file
{% for q in pillar.torii.get('sign_in', []) %}
torii-sign-in-{{ q }}:
  cmd.run:
    - name: torii -f /etc/torii/machine.yaml sign-in-key {{ q }}
    - stdin: {{ pillar.torii.authkey }}
    - onlyif: torii -f /etc/torii/machine.yaml show --json | python3 -c "import json,sys; sys.exit(0 if any(i['hostname']=='{{ q }}' and i['state']=='NeedsLogin' for v in json.load(sys.stdin)['qubes'] for i in v.get('identities',[])) else 1)"
    - require:
      - cmd: torii-apply
{% endfor %}
```

`sign_in` lists identities by their tailnet name (a service's name, or the
qube's device name, `<machine>-<qube>` by default). Keep `authkey` in an
encrypted pillar; the tool stores it only in the proxy qube
([SW-261127-3TPX](../claims/identity.md#sw-261127-3tpx-an-identity-must-sign-in-by-a-link-a-person-approves-or-by-a-one-time-key-that-only-the-proxy-qube-keeps)).

## What to keep out of other states

On the qubes in a machine's file, torii owns `netvm`, the `ipv6` feature and
the `torii-*` tags and features; on its own qubes (the proxy qubes and its
template), everything. Another state that sets these
would undo `apply`, and `apply` would undo it. `torii check` lists such
drift ([SW-261127-SMYC](../claims/seeing.md#sw-261127-smyc-check-names-each-difference-it-looks-for-between-the-live-machine-and-the-file-and-fails-on-one)). Exactly what torii touches is
[SW-261127-G26T](../claims/apply.md#sw-261127-g26t-apply-changes-only-what-torii-owns-its-template-its-proxy-qubes-its-policy-files-and-named-workload-settings).

## Not there yet

- `torii plan` exits 0 whether or not there are changes; the `unless` above
  reads its output instead. An exit code for "changes pending" would be
  cleaner.
- In `test=True`, Salt shows only that `torii-apply` would run, not the plan.
  Run `torii -f /etc/torii/machine.yaml plan` to see it.
