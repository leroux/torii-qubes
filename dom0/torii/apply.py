"""Apply: make each planned change, in order, stopping at the first that
fails. Fix the cause and run apply again; finished steps are not in the
next plan.
"""

from __future__ import annotations

import os
import shlex
import subprocess
import time
from typing import Callable

from . import derive, status
from .plan import Step

# Packages the proxy needs in a minimal template, by distribution.
DEPS = {
    "debian": "apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -y -q "
              "--no-install-recommends qubes-core-agent-networking nftables kmod procps",
    "fedora": "dnf -y -q install qubes-core-agent-networking nftables kmod procps-ng",
}
INSTALL = {
    "debian": "dpkg -i {f} || (DEBIAN_FRONTEND=noninteractive apt-get install -f -y -q && dpkg -i {f})",
    "fedora": "rpm -U --replacepkgs {f}",
}


class ApplyError(Exception):
    def __init__(self, step: Step, cause: str):
        super().__init__(f"{step}: {cause}")
        self.step = step


class Applier:
    def __init__(self, app, policy_client, bundle_dir: str, out: Callable[[str], None] = print,
                 ready_timeout: float = 180):
        self.app = app
        self.policy = policy_client
        self.bundle_dir = bundle_dir
        self.out = out
        self.ready_timeout = ready_timeout

    def run(self, steps: list[Step]) -> None:
        for i, s in enumerate(steps, 1):
            self.out(f"[{i}/{len(steps)}] {s}")
            try:
                getattr(self, "do_" + s.kind.replace("-", "_"))(s)
            except ApplyError:
                raise
            except Exception as e:  # noqa: BLE001 - report the step, whatever broke
                raise ApplyError(s, f"{type(e).__name__}: {e}") from e

    def vm(self, name):
        return self.app.domains[name]

    # --- qubes ---------------------------------------------------------
    def do_template_install(self, s: Step) -> None:
        r = subprocess.run(["qvm-template", "--yes", "install", s.target], capture_output=True, text=True)
        if r.returncode:
            raise ApplyError(s, r.stderr.strip() or "qvm-template failed")
        self.app.domains.refresh_cache(force=True)

    def do_clone(self, s: Step) -> None:
        vm = self.app.clone_vm(self.vm(s.data["source"]), s.target)
        vm.tags.add(derive.OWNED_TAG)

    def do_create(self, s: Step) -> None:
        vm = self.app.add_new_vm("AppVM", name=s.target, label=s.data["label"],
                                 template=self.vm(s.data["template"]))
        vm.tags.add(derive.OWNED_TAG)
        if s.data.get("mesh_tag"):
            vm.tags.add(s.data["mesh_tag"])

    def do_remove(self, s: Step) -> None:
        vm = self.vm(s.target)
        if vm.is_running():
            vm.shutdown(force=True)
            _wait(lambda: vm.is_halted(), 60, s, "it did not shut down")
        del self.app.domains[s.target]

    def do_restart(self, s: Step) -> None:
        """Restart a proxy qube, as Qube Manager restarts a netvm: running
        clients are detached first and reattached after, since Qubes will
        not shut down a netvm that running qubes use."""
        vm = self.vm(s.target)
        clients = [self.vm(c) for c in s.data.get("clients", [])]
        for c in clients:
            c.netvm = None
        try:
            if vm.is_running():
                vm.shutdown()
                _wait(lambda: vm.is_halted(), 120, s, "it did not shut down")
            if s.data.get("template"):
                vm.template = self.vm(s.data["template"])
            vm.start()
            vm.features[derive.STARTED_VERSION_FEATURE] = s.data["package"]
        finally:
            for c in clients:
                c.netvm = vm

    def do_pref(self, s: Step) -> None:
        v = s.data["value"]
        if s.data["key"] == "netvm" and v == derive.DEFAULT_NETVM:
            delattr(self.vm(s.target), "netvm")  # back to Qubes' default
            return
        if s.data["key"] == "netvm" and v == "":
            v = None
        setattr(self.vm(s.target), s.data["key"], v)

    def do_feature_set(self, s: Step) -> None:
        self.vm(s.target).features[s.data["key"]] = s.data["value"]

    def do_feature_del(self, s: Step) -> None:
        del self.vm(s.target).features[s.data["key"]]

    def do_tag_add(self, s: Step) -> None:
        self.vm(s.target).tags.add(s.data["tag"])

    def do_tag_del(self, s: Step) -> None:
        self.vm(s.target).tags.discard(s.data["tag"])

    # --- the proxy template ---------------------------------------------
    def do_template_package(self, s: Step) -> None:
        vm = self.vm(s.target)
        dist = vm.features.get("os-distribution", "debian")
        if dist not in DEPS:
            dist = "fedora" if "fedora" in dist else "debian"
        ext = "deb" if dist == "debian" else "rpm"
        pkg = os.path.join(self.bundle_dir, f"torii-proxy.{ext}")
        with open(pkg, "rb") as f:
            data = f.read()
        was_running = vm.is_running()
        if not was_running:
            vm.start()
        try:
            self._root(vm, DEPS[dist], s, "installing the proxy's dependencies")
            dest = f"/tmp/torii-proxy.{ext}"
            self._root(vm, f"cat > {dest}", s, "copying the package in", input=data)
            self._root(vm, INSTALL[dist].format(f=dest), s, "installing the package")
            self._root(vm, "systemctl enable torii-guard.service torii-proxy.service", s, "enabling the services")
        finally:
            vm.shutdown()
            _wait(lambda: vm.is_halted(), 120, s, "the template did not shut down")
        vm.features["torii-version"] = s.data["version"]

    def _root(self, vm, cmd: str, s: Step, what: str, input: bytes | None = None) -> str:
        """Run cmd as root in vm. With input, cmd is exec'd and reads it:
        bash reads a pipe a byte at a time, so the bytes after the first
        line reach the command untouched."""
        if input is None:
            payload = f"set -e\n{cmd}\n".encode()
        else:
            payload = f"exec sh -c {shlex.quote(cmd)}\n".encode() + input
        p = vm.run_service("qubes.VMRootShell", user="root")
        out, err = p.communicate(payload)
        if p.returncode:
            tail = (err or out or b"").decode(errors="replace").strip().splitlines()[-5:]
            raise ApplyError(s, f"{what} failed (exit {p.returncode}): " + " | ".join(tail))
        return out.decode(errors="replace")

    # --- proxy config and readiness --------------------------------------
    def do_proxy_config(self, s: Step) -> None:
        vm = self.vm(s.target)
        feats = s.data["features"]
        head = derive.CONFIG_FEATURE
        # Parts first, the head (count and hash) last, stale parts after:
        # a reader in between sees a hash mismatch and waits.
        for k in sorted(feats):
            if k != head and feats[k] is not None:
                vm.features[k] = feats[k]
        vm.features[head] = feats[head]
        for k in sorted(feats):
            if feats[k] is None and k in vm.features:
                del vm.features[k]

    def do_proxy_ready(self, s: Step) -> None:
        vm = self.vm(s.target)
        if not vm.is_running():
            vm.start()
            vm.features[derive.STARTED_VERSION_FEATURE] = s.data["package"]
        want = s.data["version"]
        deadline = time.monotonic() + self.ready_timeout
        last = "no answer"
        links_shown: set[str] = set()
        while time.monotonic() < deadline:
            try:
                st = status.read(vm)
            except status.StatusError as e:
                last = str(e)
                time.sleep(2)
                continue
            for w, link in sorted(status.sign_in_links(st).items()):
                if link not in links_shown:
                    links_shown.add(link)
                    self.out(f"    sign in {w}: {link}")
            got = st.get("status", {}).get("config_version")
            if got == want:
                return
            last = f"runs config {got}"
            time.sleep(2)
        raise ApplyError(s, f"not ready after {self.ready_timeout:.0f}s ({last})")

    # --- policy ----------------------------------------------------------
    def do_policy(self, s: Step) -> None:
        self.policy.policy_replace(s.target, s.data["text"], token="any")


def _wait(cond: Callable[[], bool], timeout: float, s: Step, msg: str) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if cond():
            return
        time.sleep(1)
    raise ApplyError(s, msg)
