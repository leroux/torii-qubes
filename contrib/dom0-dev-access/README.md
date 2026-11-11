# dom0 access from a development qube

Two small scripts to reach dom0 from a dev qube, for developing anything
that has to run in or change dom0 (Qubes tools, qrexec policy, Salt states,
templates):

- `setup-dev-access.sh`, run once in dom0, grants the dev qube a dom0 shell
  and, optionally, the Admin API.
- `d0`, run in the dev qube, runs a command or a script in that shell.

> **This gives the dev qube full control of the machine.** A shell in dom0
> and the read-write Admin API each amount to root over every qube. Do it on
> a development machine, from a dev qube you'd trust with dom0, and take it
> away (`--uninstall`) when you're done. Never on a machine holding data you
> can't afford to lose.

## What it grants

1. **A dom0 shell.** A qrexec service `qubes.VMShell` in dom0 that runs
   `/bin/bash`, and one policy line allowing it from the dev qube
   (`/etc/qubes/policy.d/30-dev-access.policy`). `d0` uses it, and
   `qvm-run --pass-io dom0 '...'` from the dev qube works too.
2. **The Admin API** (skip with `--shell-only`). The dev qube goes into the
   standard include files `admin-local-rwx`, `admin-global-rwx` and
   `admin-policy-rwx` under `/etc/qubes/policy.d/include/`, and
   `qubes-core-admin-client` goes into its template. Then `qvm-ls`,
   `qvm-prefs`, `qvm-features`, `qvm-create` and the qrexec `PolicyClient`
   work from the dev qube against the live machine. This grants nothing the
   shell doesn't already give; it's there for speed, and for real `qvm-*`
   exit codes and error messages.

Nothing else is needed. In particular: no `qubes.VMExec` in dom0, no
`user=root` (dom0 services run as dom0's user, who has passwordless `sudo`),
and no `ask` prompts, which block a non-interactive session. Use `--ask` only
if you want to approve every shell by hand.

## Set up

Read the script from the dev qube first. Then in a **dom0 terminal**, pull it
from the dev qube (here `my-dev`, with the script in `~/src/dom0-dev-access`)
and run it:

```sh
qvm-run --pass-io my-dev 'cat ~/src/dom0-dev-access/setup-dev-access.sh' > setup-dev-access.sh
less setup-dev-access.sh                      # read it: this is dom0
bash setup-dev-access.sh my-dev --dry-run     # what it would change
bash setup-dev-access.sh my-dev               # do it
```

Restart the dev qube if the script installed `qubes-core-admin-client` in
its template. Then, from the dev qube:

```sh
./d0 'hostname; id'     # dom0 / uid=1000(user) ...
qvm-ls dom0             # Admin API
```

To take it all away again, in dom0: `bash setup-dev-access.sh my-dev --uninstall`.
It leaves alone a dom0 `qubes.VMShell` it didn't create, and removes its own
only when no other policy file still allows it.

## Using `d0`

```sh
d0 'qvm-ls --fields NAME,STATE,NETVM'
d0 < some-script.sh
d0 <<'EOF'
sudo journalctl -u qubesd --since -5min --no-pager | tail
qvm-run --pass-io -u root my-test-qube 'nft list ruleset' </dev/null
EOF
```

Output and exit status are dom0's. The script arrives on the shell's stdin,
so any command in it that reads stdin (`qvm-run`, `ssh`, `dnf` without `-y`)
will swallow the rest of the script: give such commands `</dev/null`. Quote
the here-document delimiter (`'EOF'`) so that `$vars` expand in dom0, not in
the dev qube.

To copy a file from the dev qube into dom0, have dom0 pull it:

```sh
d0 "qvm-run --pass-io $(qubesdb-read /name) 'cat $PWD/build/thing.rpm' </dev/null > /tmp/thing.rpm"
```

## Tips

- **Nothing interactive.** Use `--no-pager`, `-y` and `sudo -n` (which fails
  rather than hangs) in anything you send through `d0`.
- **Long jobs** (`qvm-template install`, a template update) can outlive your
  terminal or tool timeout. Run them in the background in dom0 and poll the
  log:
  `d0 'nohup sudo -n qvm-template --yes install fedora-44-minimal > /tmp/tpl.log 2>&1 </dev/null &'`,
  then `d0 'tail -c 400 /tmp/tpl.log'`.
- **Prefer the Admin API for reading state** (`qvm-ls`, `qvm-prefs`) from the
  dev qube, and use `d0` for what only dom0 can do.
- **Name what you create** with a common prefix (`test-*` qubes,
  `40-test-*.policy` files), so it's easy to find and remove.
