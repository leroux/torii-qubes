"""The plan: every change apply would make, in the order it makes them.

Computed from the machine file and a snapshot only, so ``plan`` changes
nothing and golden tests replay recorded snapshots.

Order: the proxy template, then each proxy qube (created, configured,
started), then the policy file, then workloads, then cleanup. A workload
is switched to a proxy only after the proxy is up, and a proxy is removed
only after nothing uses it.
"""

from __future__ import annotations

import dataclasses
from typing import Any

from . import derive
from .live import Snapshot
from .machine import Machine


class PlanError(Exception):
    """The file cannot be applied to this machine as it is."""


@dataclasses.dataclass
class Step:
    kind: str
    target: str
    text: str
    data: dict[str, Any] = dataclasses.field(default_factory=dict)

    def __str__(self) -> str:
        return f"{self.target}: {self.text}"


def _features_steps(target: str, have: dict[str, str], want: dict[str, str | None], what: str) -> list[Step]:
    out = []
    for k in sorted(want):
        v = want[k]
        if v is None:
            if k in have:
                out.append(Step("feature-del", target, f"remove {what} {k}", {"key": k}))
        elif have.get(k) != v:
            shown = v if len(v) <= 60 else v[:57] + "..."
            out.append(Step("feature-set", target, f"set {what} {k} = {shown}", {"key": k, "value": v}))
    return out


def _service_steps(target: str, have: dict[str, str], want: dict[str, bool]) -> list[Step]:
    out = []
    for s, on in sorted(want.items()):
        key = f"service.{s}"
        cur = have.get(key)
        if on and cur != "1":
            out.append(Step("feature-set", target, f"enable service {s}", {"key": key, "value": "1"}))
        if not on and cur is not None:
            out.append(Step("feature-del", target, f"disable service {s}", {"key": key}))
    return out


def make(m: Machine, snap: Snapshot, bundle_version: str) -> list[Step]:
    steps: list[Step] = []
    q = snap.qubes
    errors = []
    MT = derive.managed_tag(m)

    for name, wq in sorted(m.qubes.items()):
        if name not in q:
            errors.append(f"{name} is in the file but not on this machine; the tool never creates workloads")
            continue
        info = q[name]
        if derive.OWNED_TAG in info.tags:
            errors.append(f"{name} is one of the tool's own qubes and cannot be a workload")
        others = [t for t in info.tags if derive.is_managed([t]) and t != MT]
        if others:
            errors.append(f"{name} is managed by another mesh ({others[0]}); take it out of that file first")
        if info.klass in ("TemplateVM", "AdminVM"):
            errors.append(f"{name} is a {info.klass}; only app qubes can be workloads")
        if info.provides_network:
            errors.append(f"{name} provides network to other qubes; netvms are not workloads")
        if wq.network not in ("none", "proxy", "default"):
            nv = q.get(wq.network)
            if nv is None or not nv.provides_network:
                errors.append(f"{name}.network: {wq.network} is not a netvm on this machine")
        if wq.network_card and not info.address:
            errors.append(f"{name}: routed {info.klass}s are not supported (their address changes per start)")
        for li in wq.local_inbound:
            for s in li.sources:
                if s not in q:
                    errors.append(f"{name}.local-inbound: source {s} is not on this machine")
    for x in m.services.values():
        if x.qube not in q:
            errors.append(f"services.{x.name}: its qube {x.qube} is not on this machine")
        elif derive.OWNED_TAG in q[x.qube].tags or q[x.qube].klass in ("TemplateVM", "AdminVM"):
            errors.append(f"services.{x.name}: {x.qube} cannot host services")
    for p in m.proxies.values():
        if p.netvm not in q:
            errors.append(f"proxy {p.name}: its netvm {p.netvm} is not on this machine")
        elif p.name in q and derive.OWNED_TAG not in q[p.name].tags:
            errors.append(f"proxy {p.name}: a qube of that name exists and is not the tool's")
        elif p.name in q and not derive.owned_by(m, q[p.name].tags):
            errors.append(f"proxy {p.name}: it belongs to another mesh")
    if errors:
        raise PlanError("\n".join(errors))

    # 1. The proxy template.
    tpl = m.template
    if tpl not in q:
        if m.base_template not in q:
            steps.append(Step("template-install", m.base_template,
                              "download the base template (qvm-template install)"))
        steps.append(Step("clone", tpl, f"create the proxy template, a clone of {m.base_template}",
                          {"source": m.base_template}))
    elif derive.OWNED_TAG not in q[tpl].tags:
        raise PlanError(f"template {tpl} exists and is not the tool's; name another in `template:`")
    have_v = q[tpl].features.get("lintel-version") if tpl in q else None
    if have_v != bundle_version:
        steps.append(Step("template-package", tpl,
                          f"install the proxy package {bundle_version}" + (f" (has {have_v})" if have_v else ""),
                          {"version": bundle_version}))

    addresses = {n: q[n].address for n, wq in m.qubes.items() if wq.network_card}
    version = derive.config_version(m, addresses)

    package_changes = any(s.kind == "template-package" for s in steps)

    # 2. Proxy qubes.
    for p in sorted(m.proxies.values(), key=lambda p: p.name):
        cur = q.get(p.name)
        # Running on another package than the bundle's: this apply upgrades
        # the template, or something else did since the proxy started.
        stale = cur is not None and cur.features.get(derive.STARTED_VERSION_FEATURE) != bundle_version
        # A running qube's template cannot change: moving it is a restart.
        moving = cur is not None and cur.template != tpl and cur.state == "Running"
        if (package_changes or stale or moving) and cur is not None and cur.state == "Running":
            clients = sorted(n for n, i in q.items() if i.netvm == p.name and i.state == "Running")
            detach = f" (detaching and reattaching {', '.join(clients)})" if clients else ""
            data = {"clients": clients, "package": bundle_version}
            if moving:
                data["template"] = tpl
            steps.append(Step("restart", p.name,
                              (f"move it to {tpl} and restart it" if moving else "restart it on the new package") + detach,
                              data))
        if cur is None:
            steps.append(Step("create", p.name,
                              f"create the proxy qube (template {tpl}, netvm {p.netvm}, provides network)",
                              {"template": tpl, "label": p.label,
                               "mesh_tag": derive.MESH_TAG + m.mesh if m.mesh else ""}))
            have_f: dict[str, str] = {}
        else:
            have_f = cur.features
        prefs = {"template": tpl, "netvm": p.netvm, "provides_network": True,
                 "autostart": True, "label": p.label}
        # No IPv6 behind a proxy qube: an empty ipv6 feature is false, and
        # its clients inherit it, whatever the netvms above say.
        if have_f.get("ipv6") != "":
            steps.append(Step("feature-set", p.name, "turn Qubes' IPv6 off for it and its clients",
                              {"key": "ipv6", "value": ""}))
        for k, v in prefs.items():
            curv = None if cur is None else {"template": cur.template, "netvm": cur.netvm,
                                             "provides_network": cur.provides_network,
                                             "autostart": cur.autostart, "label": cur.label}[k]
            if cur is None and k in ("template", "label"):
                continue  # set at creation
            if k == "template" and moving:
                continue  # the restart moves it
            if curv != v:
                steps.append(Step("pref", p.name, f"set {k} = {v}", {"key": k, "value": v}))
        steps += _service_steps(p.name, have_f, {derive.PROXY_SERVICE: True})
        cfg = derive.proxy_config(m, p.name, addresses, version)
        want = dict(derive.encode_chunks(cfg))
        n = int(want[derive.CONFIG_FEATURE].split()[0])
        for k in have_f:
            if k.startswith(derive.CONFIG_FEATURE + ".") and k[len(derive.CONFIG_FEATURE) + 1:].isdigit() \
                    and int(k[len(derive.CONFIG_FEATURE) + 1:]) >= n:
                want[k] = None
        changed = any(have_f.get(k) != v for k, v in want.items())
        if changed:
            nw = len(cfg["workloads"])
            steps.append(Step("proxy-config", p.name,
                              f"set its config {version}: {nw} workload{'s' if nw != 1 else ''}",
                              {"features": want, "version": version, "config": cfg}))
        if cur is None or changed or cur.state != "Running":
            steps.append(Step("proxy-ready", p.name,
                              "start it and wait until it runs config " + version
                              if cur is None or cur.state != "Running"
                              else "wait until it has loaded config " + version,
                              {"version": version, "package": bundle_version}))

    # 3. The policy files: this mesh's, and the shared denies after them.
    want_pol = derive.policy(m)
    if snap.policy != want_pol:
        steps.append(Step("policy", derive.policy_name(m),
                          "write the qrexec policy" if snap.policy is None else "replace the qrexec policy",
                          {"text": want_pol, "old": snap.policy or ""}))
    deny = derive.deny_policy()
    if snap.other_policy.get(derive.DENY_POLICY) != deny:
        steps.append(Step("policy", derive.DENY_POLICY, "write the shared qrexec denies",
                          {"text": deny, "old": snap.other_policy.get(derive.DENY_POLICY, "")}))

    # 4. Workloads in the file.
    for name, wq in sorted(m.qubes.items()):
        info = q[name]
        st = derive.qube_state(m, wq)
        if st.netvm == derive.DEFAULT_NETVM:
            if not info.netvm_default:
                steps.append(Step("pref", name, f"reset netvm to Qubes' default (was {info.netvm or '(none)'})",
                                  {"key": "netvm", "value": st.netvm}))
        elif st.netvm is not None and (info.netvm != st.netvm or info.netvm_default):
            steps.append(Step("pref", name, f"set netvm = {st.netvm or '(none)'}"
                              + (f" (was {info.netvm or '(none)'})"), {"key": "netvm", "value": st.netvm}))
        if MT not in info.tags:
            steps.append(Step("tag-add", name, f"tag {MT}", {"tag": MT}))
        if derive.KEEP_TAG in info.tags:
            steps.append(Step("tag-del", name, f"untag {derive.KEEP_TAG}", {"tag": derive.KEEP_TAG}))
        steps += _service_steps(name, info.features, st.services)
        steps += _features_steps(name, info.features, st.features, "feature")

    # 5. Qubes the tool manages that left the file: the machine's safe default.
    gone = sorted(n for n, i in q.items()
                  if MT in i.tags and n not in m.qubes)
    if m.manage == "all":
        gone = sorted(set(gone) | {n for n, i in q.items()
                                   if n not in m.qubes and i.klass in ("AppVM", "StandaloneVM")
                                   and not i.provides_network and derive.OWNED_TAG not in i.tags
                                   and not derive.is_managed(i.tags)})
    for name in gone:
        info = q[name]
        safe = m.safe_default(name)
        if m.manage == "all":
            st = derive.qube_state(m, safe)
        else:
            st = derive.reset_state()
            st.netvm = m.proxies[safe.proxy].name if safe.network_card else ""
        if derive.KEEP_TAG in info.tags:
            st.netvm = info.netvm  # it was never the tool's to set
        if info.netvm != st.netvm:
            steps.append(Step("pref", name, f"reset netvm = {st.netvm or '(none)'} (was {info.netvm or '(none)'})",
                              {"key": "netvm", "value": st.netvm}))
        steps += _service_steps(name, info.features, st.services)
        steps += _features_steps(name, info.features, st.features, "feature")
        if not st.managed and MT in info.tags:
            steps.append(Step("tag-del", name, f"untag {MT} (left the file)", {"tag": MT}))
            if derive.KEEP_TAG in info.tags:
                steps.append(Step("tag-del", name, f"untag {derive.KEEP_TAG}", {"tag": derive.KEEP_TAG}))
        elif st.managed and MT not in info.tags:
            steps.append(Step("tag-add", name, f"tag {MT}", {"tag": MT}))

    # 6. The tool's own qubes the file no longer names.
    for name, info in sorted(q.items()):
        if derive.owned_by(m, info.tags) and info.klass == "AppVM" and name not in m.proxies:
            users = [n for n, i in q.items() if i.netvm == name and n not in gone and n not in m.qubes]
            if users:
                raise PlanError(f"{name} is to be removed but still the netvm of {', '.join(sorted(users))}")
            steps.append(Step("remove", name, "remove the proxy qube (no longer in the file); its identities go with it"))

    # 7. The tool's templates nothing will use: an earlier base's, once the
    # proxies have moved. One another mesh's proxies still use stays.
    leaving = {s.target for s in steps if s.kind == "remove"}
    for name, info in sorted(q.items()):
        if info.klass != "TemplateVM" or derive.OWNED_TAG not in info.tags or name == tpl:
            continue
        users = [n for n, i in q.items() if i.template == name and n not in leaving and n not in m.proxies]
        if not users:
            steps.append(Step("remove", name, f"remove the tool's old template (nothing uses it now; {tpl} replaces it)"))
    return steps


def notes(m: Machine, snap: Snapshot) -> list[str]:
    """Things apply does not change but a person should know."""
    out = []
    need: dict[str, list[str]] = {}
    for name, wq in sorted(m.qubes.items()):
        info = snap.qubes.get(name)
        if info is None:
            continue
        st = derive.qube_state(m, wq)
        if st.services.get(derive.CLIENT_SERVICE):
            tpl = info.template or name  # a standalone qube carries its own
            t = snap.qubes.get(tpl)
            if t is None or derive.CLIENT_VERSION_FEATURE not in t.features:
                need.setdefault(tpl, []).append(name)
    for tpl, users in sorted(need.items()):
        out.append(f"{', '.join(users)} need{'s' if len(users) == 1 else ''} the lintel client in {tpl}; "
                   f"install it with `lintel install-client {tpl}`, then restart them")
    return out
