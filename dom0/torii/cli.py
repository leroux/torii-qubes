"""torii: declarative, visible connectivity for qubes.

    torii plan     [-f FILE]          every change apply would make
    torii apply    [-f FILE] [-y]     make them (asks first unless -y)
    torii show     [QUBE ...] [--json] what each qube can reach and what can reach it, live
    torii check    [-f FILE]          where the live system and the file disagree
    torii grants   [-f FILE]          proposed Tailscale tag owners and grants
    torii log      QUBE [-n N]        recent connection decisions for a qube
    torii log      --egress NAME      an egress's exit-node events: switches, nodes down and back
    torii sign-in-key NAME < key       a one-time key for a qube's, a service's or an egress's (egress:NAME)
                                        identity, stored in its proxy qube
    torii install-client TEMPLATE     the workload-side client, into a template
    torii snapshot                    the live state plan reads, as JSON (for bug reports and tests)
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import sys

from . import __version__, derive, env, live, machine, plan, show, status

DEFAULT_FILE = os.path.expanduser("~/torii.yaml")
BUNDLE_DIR = os.environ.get(env.BUNDLE, "/usr/share/torii")


def _app():
    import qubesadmin  # noqa: PLC0415 - only on Qubes
    return qubesadmin.Qubes()


def _policy_client():
    from qrexec.policy.admin_client import PolicyClient  # noqa: PLC0415
    return PolicyClient()


def _snapshot(app, m: machine.Machine | None) -> live.Snapshot:
    interesting: set[str] = set()
    if m is not None:
        interesting = set(m.qubes) | set(m.proxies) | {m.template}
        # Workloads' templates, to see which carry the client.
        for n in m.qubes:
            try:
                t = getattr(app.domains[n], "template", None)
            except Exception:  # noqa: BLE001 - no such qube: plan says so
                t = None
            if t is not None:
                interesting.add(str(t))
    return live.take(app, _policy_client(), interesting, derive.policy_name(m) if m else derive.POLICY_NAME)


def _proxy_states(app, snap: live.Snapshot) -> dict[str, dict]:
    out = {}
    for n, i in snap.qubes.items():
        if derive.OWNED_TAG not in i.tags or i.klass != "AppVM":
            continue
        d: dict = {}
        try:
            d["config"] = derive.decode_chunks(i.features)
        except Exception as e:  # noqa: BLE001
            d["config_error"] = str(e)
        if i.state == "Running":
            try:
                d["status"] = status.read(app.domains[n])
            except status.StatusError as e:
                d["status_error"] = str(e)
        out[n] = d
    return out


def cmd_plan(a) -> int:
    m = machine.load(a.file)
    app = _app()
    snap = _snapshot(app, m)
    steps = plan.make(m, snap, _bundle_version())
    for n in plan.notes(m, snap):
        print("note:", n)
    if not steps:
        print("No changes: the machine matches", a.file)
        return 0
    for s in steps:
        print(s)
        if s.kind == "policy" and a.diff:
            import difflib  # noqa: PLC0415
            sys.stdout.writelines("    " + l for l in difflib.unified_diff(
                s.data["old"].splitlines(True), s.data["text"].splitlines(True), "live", "planned"))
    print(f"\n{len(steps)} change{'s' if len(steps) != 1 else ''}. Run `torii apply` to make them.")
    return 0


def cmd_apply(a) -> int:
    from .apply import Applier, ApplyError  # noqa: PLC0415
    m = machine.load(a.file)
    app = _app()
    snap = _snapshot(app, m)
    steps = plan.make(m, snap, _bundle_version())
    notes = plan.notes(m, snap)
    if not steps:
        print("No changes: the machine matches", a.file)
        for n in notes:
            print("note:", n)
        return 0
    for s in steps:
        print(" ", s)
    if not a.yes:
        try:
            answer = input(f"Make these {len(steps)} changes? [y/N] ")
        except EOFError:  # no terminal: no answer is no
            answer = ""
            print()
        if answer.strip().lower() not in ("y", "yes"):
            print("Nothing changed. (`apply -y` does not ask.)")
            return 1
    try:
        Applier(app, _policy_client(), BUNDLE_DIR).run(steps)
    except ApplyError as e:
        print(f"\napply stopped: {e}\nFix it and run `torii apply` again; finished steps are kept.", file=sys.stderr)
        return 1
    print("Done.")
    for n in notes:
        print("note:", n)
    return 0


def cmd_show(a) -> int:
    app = _app()
    m = machine.load(a.file) if os.path.exists(a.file) else None
    snap = _snapshot(app, m)
    ps = _proxy_states(app, snap)
    names = a.qubes or sorted(n for n, i in snap.qubes.items()
                              if (derive.managed_tag(m) in i.tags if m else derive.is_managed(i.tags))
                              or (m and n in m.qubes))
    views = [show.qube_view(snap, n, ps) for n in names if n in snap.qubes]
    if a.json:
        json.dump({"qubes": views, "proxies": {p: {k: v for k, v in d.items() if k != "config"}
                                               for p, d in ps.items()}}, sys.stdout, indent=1)
        print()
        return 0
    for p, d in sorted(ps.items()):
        info = snap.qubes[p]
        st = (d.get("status") or {}).get("status") or {}
        line = f"proxy {p}: {info.state.lower()}"
        if st:
            line += f", config {st.get('config_version')}, {len(st.get('workloads') or {})} workloads"
        if d.get("status_error"):
            line += f" (status: {d['status_error']})"
        print(line)
        for w, link in sorted(status.sign_in_links(d.get("status") or {}).items()):
            print(f"  waiting for sign-in: {w}: {link}")
        for line in show.egress_lines(st) + show.unlisted_lines(snap, d.get("status") or {}):
            print(line)
    print()
    print("\n\n".join(show.format_view(v) for v in views))
    missing = [n for n in a.qubes if n not in snap.qubes]
    for n in missing:
        print(f"{n}: no such qube", file=sys.stderr)
    return 1 if missing else 0


def cmd_check(a) -> int:
    m = machine.load(a.file)
    app = _app()
    snap = _snapshot(app, m)
    problems = show.check_problems(m, snap, _proxy_states(app, snap), _bundle_version(),
                                   datetime.datetime.now(datetime.timezone.utc))
    if not problems:
        print("OK: the live system matches", a.file)
        return 0
    print("\n".join(problems))
    return 1


def cmd_grants(a) -> int:
    m = machine.load(a.file)
    print("// Proposed for the tailnet policy file. Review, narrow `src`, then paste.")
    print(json.dumps(derive.grants(m), indent=2))
    return 0


def cmd_log(a) -> int:
    if a.egress:
        a.qube = f"egress:{a.egress}"
    if not a.qube:
        print("name a QUBE, or --egress NAME", file=sys.stderr)
        return 2
    app = _app()
    snap = _snapshot(app, None)
    ps = _proxy_states(app, snap)
    found = False
    for p, d in sorted(ps.items()):
        for e in ((d.get("status") or {}).get("log") or [])[-a.n * 10:]:
            if e.get("workload") == a.qube or e.get("source") == a.qube:
                found = True
                print(json.dumps(e, sort_keys=True))
    if not found:
        print(f"no recent decisions for {a.qube}", file=sys.stderr)
    return 0


# charter:realizes SW-261127-3TPX
def cmd_sign_in_key(a) -> int:
    """Store a one-time key for a qube's identity in its proxy qube (never
    in dom0) and restart the proxy so the identity signs in with it."""
    from .apply import Applier, ApplyError  # noqa: PLC0415
    from .plan import Step  # noqa: PLC0415
    m = machine.load(a.file)
    # A qube's own identity, or a service; a service's identity state and
    # key are kept under its workload name, <qube>.<service>.
    if a.qube.startswith("egress:"):
        # An egress's identity, on one proxy: egress:<name>, or
        # egress:<name>@<proxy> when several proxies carry it.
        g, _, want = a.qube[len("egress:"):].partition("@")
        on = [p for e, p in m.egress_instances() if e == g and (not want or p == want)]
        if len(on) != 1:
            print(f"{a.qube}: " + ("not carried by any proxy in " + a.file if not on else
                                   "on several proxies; write egress:" + g + "@<one of " + ", ".join(on) + ">"),
                  file=sys.stderr)
            return 2
        workload, proxy = f"egress-{g}", on[0]
    elif a.qube in m.services:
        x = m.services[a.qube]
        workload, proxy = f"{x.qube}.{x.name}", x.proxy
    elif a.qube in m.qubes and m.qubes[a.qube].identity != "none":
        workload, proxy = a.qube, m.qubes[a.qube].proxy
    else:
        print(f"{a.qube}: no such identity in {a.file} (a qube with tailnet.identity, a service, or egress:<name>)",
              file=sys.stderr)
        return 2
    key = sys.stdin.readline().strip()
    if not key.startswith("tskey-") and not m.control_url:
        print("expected a key (tskey-...) on stdin", file=sys.stderr)
        return 2
    app = _app()
    ap = Applier(app, None, BUNDLE_DIR)
    vm = app.domains[proxy]
    s = Step("sign-in-key", proxy, f"key for {a.qube}")
    path = f"{derive.KEY_DIR}/{workload}"
    try:
        ap._root(vm, f"install -d -m 0700 {derive.KEY_DIR} && rm -f {path} && umask 077 && cat > {path}", s, "writing the key",
                 input=(key + "\n").encode())
        ap._root(vm, f"rm -rf {derive.STATE_DIR}/{workload} && systemctl restart torii-proxy", s,
                 "restarting the proxy")
    except ApplyError as e:
        print(e, file=sys.stderr)
        return 1
    print(f"{a.qube}: key stored in {proxy}:{path}; its identity signs in again with it")
    return 0


def cmd_install_client(a) -> int:
    """Install the torii client package into a template (or standalone qube)."""
    from .apply import DEPS, INSTALL, Applier, ApplyError  # noqa: PLC0415,F401
    from .plan import Step  # noqa: PLC0415
    app = _app()
    vm = app.domains[a.template]
    dist = vm.features.get("os-distribution", "")
    ext = "deb" if dist in ("debian", "ubuntu", "kali", "whonix") else "rpm"
    inst = INSTALL["debian" if ext == "deb" else "fedora"]
    with open(os.path.join(BUNDLE_DIR, f"torii-client.{ext}"), "rb") as f:
        data = f.read()
    ap = Applier(app, None, BUNDLE_DIR)
    s = Step("install-client", a.template, "install the client")
    running = vm.is_running()
    try:
        if not running:
            vm.start()
        ap._root(vm, f"cat > /tmp/torii-client.{ext}", s, "copying the package in", input=data)
        ap._root(vm, inst.format(f=f"/tmp/torii-client.{ext}"), s, "installing it")
    except ApplyError as e:
        print(e, file=sys.stderr)
        return 1
    finally:
        if not running and vm.klass == "TemplateVM":
            vm.shutdown()
    vm.features[derive.CLIENT_VERSION_FEATURE] = _bundle_version()
    print(f"{a.template}: torii client {_bundle_version()} installed; restart the qubes based on it")
    return 0


def cmd_snapshot(a) -> int:
    m = machine.load(a.file) if os.path.exists(a.file) else None
    print(_snapshot(_app(), m).to_json())
    return 0


def _bundle_version() -> str:
    try:
        with open(os.path.join(BUNDLE_DIR, "VERSION"), encoding="utf-8") as f:
            return f.read().strip()
    except OSError:
        return __version__


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="torii", description="Declarative, visible connectivity for qubes.")
    ap.add_argument("--version", action="version", version=__version__)
    # -f may come before the command (torii -f ~/torii-test.yaml plan) or after it.
    ap.add_argument("-f", "--file", dest="top_file", default=None, help=f"machine file (default {DEFAULT_FILE})")
    sub = ap.add_subparsers(dest="cmd", required=True)

    def add(name, fn, help_):
        p = sub.add_parser(name, help=help_)
        p.add_argument("-f", "--file", default=None, help=f"machine file (default {DEFAULT_FILE})")
        p.set_defaults(fn=fn)
        return p

    p = add("plan", cmd_plan, "show every change apply would make")
    p.add_argument("--diff", action="store_true", help="show the policy file's diff")
    p = add("apply", cmd_apply, "make the machine match the file")
    p.add_argument("-y", "--yes", action="store_true", help="do not ask")
    p = add("show", cmd_show, "what qubes can reach, read live")
    p.add_argument("qubes", nargs="*")
    p.add_argument("--json", action="store_true")
    add("check", cmd_check, "where the live system and the file disagree")
    add("grants", cmd_grants, "proposed Tailscale tag owners and grants")
    p = add("log", cmd_log, "recent connection decisions for a qube, or an egress's exit-node events")
    p.add_argument("qube", nargs="?")
    p.add_argument("--egress", metavar="NAME", help="an egress's events: switches, exit nodes down and back")
    p.add_argument("-n", type=int, default=20)
    p = add("sign-in-key", cmd_sign_in_key,
            "give an identity (a QUBE, a SERVICE, or egress:NAME) a one-time key from stdin")
    p.add_argument("qube")
    p = add("install-client", cmd_install_client, "install the torii client into a workload template")
    p.add_argument("template")
    add("snapshot", cmd_snapshot, "the live state, as JSON")
    a = ap.parse_args(argv)
    a.file = a.file or a.top_file or DEFAULT_FILE
    try:
        return a.fn(a)
    except machine.FileError as e:
        print(f"{a.file}: {e}", file=sys.stderr)
        return 2
    except plan.PlanError as e:
        print(f"cannot apply {a.file} to this machine:\n{e}", file=sys.stderr)
        return 2
    except FileNotFoundError as e:
        print(f"torii: {e}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
