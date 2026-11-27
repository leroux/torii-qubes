"""show and check: what each qube can reach and what can reach it, read
from the live system (prefs, policy files, the proxies' own configs and
status), never from the machine file.
"""

from __future__ import annotations

import dataclasses
import re
from typing import Any

from . import derive
from .live import Snapshot

POLICY_LINE = re.compile(r"^\s*(?P<svc>\S+)\s+(?P<arg>\S+)\s+(?P<src>\S+)\s+(?P<dst>\S+)\s+(?P<action>\S+)(?P<rest>.*)$")


@dataclasses.dataclass(frozen=True)
class Rule:
    file: str
    service: str
    arg: str
    source: str
    target: str  # the effective target (target= for redirects)
    action: str
    requested: str = ""  # the target the rule matches, before a redirect
    include: bool = False  # an !include line: a file show does not read

    @property
    def tokens(self) -> bool:
        """Its source or target is a token show does not evaluate, such as
        @tag:, @type:, @dispvm or @default as a source."""
        def tok(x: str) -> bool:
            return x.startswith("@") and x not in ("@anyvm", "@default")
        return tok(self.source) or tok(self.requested or self.target)


# charter:realizes SW-261127-62WV
def rules(snap: Snapshot, own: str = derive.POLICY_NAME) -> list[Rule]:
    """Every rule of every policy file; own is the name torii's file for
    the snapshot's mesh has."""
    files = dict(snap.other_policy)
    if snap.policy is not None:
        files[own] = snap.policy
    out = []
    for name, text in sorted(files.items()):
        for line in text.splitlines():
            line = line.split("#", 1)[0]
            if line.strip().startswith("!include"):
                out.append(Rule(name, "!include", "", "", line.split(None, 1)[1].strip() if len(line.split()) > 1 else "",
                                "", include=True))
                continue
            m = POLICY_LINE.match(line)
            if not m or m["svc"].startswith("!"):
                continue
            dst = m["dst"]
            t = re.search(r"\btarget=(\S+)", m["rest"])
            if t:
                dst = t.group(1)
            out.append(Rule(name, m["svc"], m["arg"], m["src"], dst, m["action"], requested=m["dst"]))
    return out


# charter:realizes SW-261127-62WV
def _allowed(rs: list[Rule], service: str, src: str, dst: str) -> dict[str, str]:
    """arg -> file, for the rules of service (or of every service, `*`)
    that let src call dst, first match deciding per argument as qrexec
    reads policy: files in name order, a rule matching on the target
    asked for, a redirect (target=) reaching only its new target, and
    `ask` shown as "<file> (ask)". Exact names and @anyvm are matched;
    other tokens are not evaluated (check names the rules that use them)."""
    seen: dict[str, str] = {}
    decided: set[str] = set()
    for r in rs:
        if r.include or r.service not in (service, "*"):
            continue
        asked = r.requested or r.target
        if r.source not in (src, "@anyvm") or asked not in (dst, "@anyvm"):
            continue
        if r.arg in decided:
            continue
        reaches = r.action in ("allow", "ask") and (r.target in (dst, asked) or dst == "@default")
        label = r.file + (" (ask)" if r.action == "ask" else "")
        if r.arg == "*":
            # A wildcard decides every argument not yet decided; we only list
            # explicit ones and mark a wildcard allow loudly.
            if reaches:
                seen["*"] = label
            break
        decided.add(r.arg)
        if reaches:
            seen[r.arg] = label
    return seen


# charter:realizes SW-261127-62WV
def qube_view(snap: Snapshot, name: str, proxy_states: dict[str, dict[str, Any]]) -> dict[str, Any]:
    info = snap.qubes[name]
    rs = rules(snap)
    proxies = {n for n, i in snap.qubes.items() if derive.OWNED_TAG in i.tags and i.klass == "AppVM"}
    v: dict[str, Any] = {"qube": name, "netvm": info.netvm or None,
                         "network_card": bool(info.netvm), "managed": derive.is_managed(info.tags)}
    if derive.PRIOR_NETVM_FEATURE in info.features:
        v["adopted"] = {"from": info.features[derive.PRIOR_NETVM_FEATURE],
                        "on": info.features.get(derive.ADOPTED_FEATURE, "")}
    # Which proxy holds it, by its live config.
    cfg = None
    for p in sorted(proxies):
        c = proxy_states.get(p, {}).get("config") or {}
        if name in (c.get("workloads") or {}):
            cfg, v["proxy"] = c["workloads"][name], p
            break
    st = None
    if cfg is not None:
        st = ((proxy_states[v["proxy"]].get("status") or {}).get("status") or {}).get("workloads", {}).get(name)
    if info.netvm in proxies:
        v["link"] = "routed"
    elif cfg is not None and not info.netvm:
        v["link"] = "qrexec"
    elif info.netvm:
        v["link"] = "netvm"  # not behind a proxy qube: whatever its netvm allows
    else:
        v["link"] = "offline"

    ident = (cfg or {}).get("identity")
    if ident:
        v["identity"] = {"hostname": ident.get("hostname"),
                         "kind": "login" if ident.get("login") else "tagged",
                         "tags": ident.get("tags", []),
                         "state": ((st or {}).get("identity") or {}).get("state", "unknown"),
                         "addresses": ((st or {}).get("identity") or {}).get("ips", [])}
    inbound = []
    for i in (cfg or {}).get("inbound", []):
        port, target = i["port"], i.get("target", i["port"])
        if v["link"] in ("qrexec", "routed"):
            by = _allowed(rs, "qubes.ConnectTCP", v["proxy"], name).get(f"+{target}")
            inbound.append({"port": port, "target": target, "reachable": by is not None, "policy": by})
        else:
            inbound.append({"port": port, "target": target, "reachable": True})
    v["tailnet_inbound"] = inbound
    v["tailnet_outbound"] = bool((cfg or {}).get("tailnet_outbound"))
    if (cfg or {}).get("tailnet_allow"):
        v["tailnet_allow"] = cfg["tailnet_allow"]
    # The proxy's config says what the qube reaches only when its traffic
    # goes through that proxy; on any other netvm it reaches what that
    # netvm allows, whatever the proxy holds for it.
    if cfg is not None and v["link"] in ("routed", "qrexec"):
        for zone in ("internet", "lan"):
            mode = cfg.get(zone, "none")
            v[zone] = "all" if mode == "full" else mode
            if cfg.get(f"{zone}_allow"):
                v[f"{zone}_allow"] = cfg[f"{zone}_allow"]
        v["internet_via"] = cfg.get("egress") or "direct"
    elif info.netvm:
        v["internet"] = f"as {info.netvm} allows (qvm-firewall)"
    else:
        v["internet"] = "none"
    if v["link"] == "qrexec":
        calls = {}
        for a, f in _allowed(rs, "torii.Connect", name, v["proxy"]).items():
            calls[a] = f
        for a, f in _allowed(rs, "torii.Connect", name, "@default").items():
            calls.setdefault(a, f)
        v["outbound_calls"] = sorted(calls)
    # Local: qubes.ConnectTCP between qubes on this machine (any file).
    v["local_inbound"] = []
    v["local_outbound"] = []
    for other in sorted(snap.qubes):
        if other == name or other in proxies:
            continue
        for a, f in sorted(_allowed(rs, "qubes.ConnectTCP", other, name).items()):
            v["local_inbound"].append({"from": other, "port": a.lstrip("+"), "policy": f})
        for a, f in sorted(_allowed(rs, "qubes.ConnectTCP", name, other).items()):
            v["local_outbound"].append({"to": other, "port": a.lstrip("+"), "policy": f})
    # Extra identities: workloads of a proxy whose qube is this one.
    v["identities"] = []
    for p in sorted(proxies):
        ps = proxy_states.get(p, {})
        pst = ((ps.get("status") or {}).get("status") or {}).get("workloads", {})
        for wname, wc in sorted(((ps.get("config") or {}).get("workloads") or {}).items()):
            if wc.get("qube") != name:
                continue
            ist = (pst.get(wname) or {}).get("identity") or {}
            allowed = _allowed(rs, "qubes.ConnectTCP", p, name)
            v["identities"].append({
                "hostname": wc["identity"]["hostname"], "tags": wc["identity"].get("tags", []),
                "state": ist.get("state", "unknown"), "addresses": ist.get("ips", []), "proxy": p,
                "inbound": [{"port": i["port"], "target": i.get("target", i["port"]), "tls": bool(i.get("tls")),
                             "reachable": f"+{i.get('target', i['port'])}" in allowed or "*" in allowed}
                            for i in wc.get("inbound", [])]})
    v["pattern"] = pattern(v)
    if st is not None:
        v["active_connections"] = st.get("active_connections", 0)
    return v


def pattern(v: dict[str, Any]) -> str:
    link, ident = v["link"], v.get("identity")
    if link == "offline":
        return "no network card, hosts services" if v.get("identities") else "no network card"
    if link == "netvm":
        return "on its own netvm" + (", hosts services" if v.get("identities") else "")
    out = v.get("tailnet_outbound") or v.get("internet") not in (None, "none") or v.get("lan") not in (None, "none")
    if link == "qrexec":
        if ident and v["tailnet_inbound"] and not out:
            return "no network card, serves the tailnet"
        if out:
            return "no network card, with outbound" + (" and inbound" if v["tailnet_inbound"] else "")
        return "no network card"
    if link == "routed":
        if v.get("internet") == "none" and v.get("tailnet_outbound"):
            return "tailnet-only client"
        return "routed through " + v.get("proxy", "?")
    return link


def format_view(v: dict[str, Any]) -> str:
    lines = [f"{v['qube']}  ({v['pattern']})"]
    nc = "yes, netvm " + v["netvm"] if v["netvm"] else "no"
    lines.append(f"  network card   {nc}")
    if v.get("proxy"):
        lines.append(f"  proxy qube     {v['proxy']} ({v['link']})")
    ident = v.get("identity")
    if ident:
        tags = " " + ",".join(ident["tags"]) if ident["tags"] else ""
        addrs = " " + " ".join(ident["addresses"]) if ident["addresses"] else ""
        lines.append(f"  identity       {ident['hostname']} ({ident['kind']}{tags}) {ident['state']}{addrs}")
    elif not v.get("identities"):
        lines.append("  identity       none")
    if v["tailnet_inbound"]:
        ps = []
        for i in v["tailnet_inbound"]:
            s = str(i["port"]) + (f"->{i['target']}" if i["target"] != i["port"] else "")
            if not i["reachable"]:
                s += " (no policy: unreachable)"
            ps.append(s)
        lines.append(f"  tailnet in     {', '.join(ps)} (who: the tailnet's grants)")
    if v.get("tailnet_allow"):
        lines.append(f"  tailnet out    only {', '.join(v['tailnet_allow'])} (and what the grants allow)")
    else:
        lines.append(f"  tailnet out    {'yes (where: the grants)' if v['tailnet_outbound'] else 'no'}")
    inet = v.get("internet", "none")
    if v.get("internet_allow"):
        inet += ": " + ", ".join(v["internet_allow"])
    if v.get("internet_via") not in (None, "direct"):
        inet += f" via egress {v['internet_via']}"
    lines.append(f"  internet       {inet}")
    if "lan" in v:
        lan = v["lan"]
        if v.get("lan_allow"):
            lan += ": " + ", ".join(v["lan_allow"])
        lines.append(f"  lan            {lan}")
    if v.get("outbound_calls"):
        lines.append(f"  calls proxy    {' '.join(v['outbound_calls'])}")
    for li in v["local_inbound"]:
        lines.append(f"  local in       {li['from']} -> port {li['port']} ({li['policy']})")
    for lo in v["local_outbound"]:
        lines.append(f"  local out      -> {lo['to']} port {lo['port']} ({lo['policy']})")
    for x in v.get("identities", []):
        tags = ",".join(x["tags"])
        addrs = " " + " ".join(x["addresses"]) if x["addresses"] else ""
        ports = ", ".join(f"{i['port']}{'/tls' if i['tls'] else ''}->{i['target']}"
                          + ("" if i["reachable"] else " (no policy: unreachable)") for i in x["inbound"])
        lines.append(f"  service        {x['hostname']} ({tags}) {x['state']}{addrs} via {x['proxy']}: in {ports}")
    if "active_connections" in v:
        lines.append(f"  connections    {v['active_connections']} active")
    if v.get("adopted"):
        a = v["adopted"]
        lines.append(f"  adopted        from netvm {a['from']}" + (f" on {a['on']}" if a["on"] else ""))
    return "\n".join(lines)


# --- egresses and unlisted sources, from a proxy's status ------------------

def _when(ts: str) -> str:
    """An RFC 3339 time from the proxy, shortened: 2026-11-08 10:02 UTC."""
    if not ts:
        return "?"
    return ts[:10] + " " + ts[11:16] + " UTC"


def _down(g: dict[str, Any]) -> str:
    return ", ".join(f"{x['name']}: {x.get('why') or 'down'}" for x in g.get("exits") or [] if not x.get("healthy"))


def egress_lines(st: dict[str, Any]) -> list[str]:
    """Each egress of a proxy's status: the exit node in use, and why the
    others are down."""
    out = []
    for name, g in sorted((st.get("egresses") or {}).items()):
        if g.get("usable"):
            head = f"  egress {name}: active {g.get('active')} since {_when(g.get('active_since', ''))}"
        else:
            last = f"; last {g['active']}" if g.get("active") else ""
            head = f"  egress {name}: NO HEALTHY EXIT NODE (connections refused{last})"
        out.append(head)
        for x in g.get("exits") or []:
            out.append(f"    {x['name']}  " + ("up" if x.get("healthy") else f"down ({x.get('why') or '?'})"))
    return out


def unlisted_lines(snap: Snapshot, read: dict[str, Any]) -> list[str]:
    """Qubes attached to a proxy but not in its config, from its recent log
    (read: what torii.Status returned):
    refused ones (workload "-") and those its unlisted: lets through. dom0
    names them by address; the proxy never knows their names."""
    by_ip = {i.address: n for n, i in snap.qubes.items() if i.address}
    counts: dict[str, list[int]] = {}
    for e in read.get("log") or []:
        w = e.get("workload", "")
        if w == "-" and e.get("reason") == "unknown-workload":
            ip, allowed = e.get("source", ""), False
        elif w.startswith("unlisted:"):
            ip, allowed = w[len("unlisted:"):], e.get("verdict") == "allow"
        else:
            continue
        c = counts.setdefault(ip, [0, 0])
        c[0 if allowed else 1] += 1
    return [f"  unlisted {by_ip.get(ip, '?')} ({ip}): {a} allowed, {r} refused"
            for ip, (a, r) in sorted(counts.items(), key=lambda kv: by_ip.get(kv[0], kv[0]))]


def egress_problems(proxy: str, st: dict[str, Any], now) -> list[str]:
    """What check reports of a proxy's egresses: an identity not signed in,
    no healthy exit node, or more than a day on a fallback node."""
    import datetime  # noqa: PLC0415
    out = []
    for name, g in sorted((st.get("egresses") or {}).items()):
        where = f"egress {name} on {proxy}"
        ident = g.get("identity") or {}
        if ident and ident.get("state") != "Running":
            out.append(f"{where}: identity {ident.get('hostname')} is {ident.get('state')}"
                       + (f"; sign in: {ident['auth_url']}" if ident.get("auth_url") else ""))
        exits = [x["name"] for x in g.get("exits") or []]
        if not g.get("usable"):
            out.append(f"{where}: no healthy exit node; its qubes' internet is refused ({_down(g)})")
        elif exits and g.get("active") != exits[0] and g.get("active_since"):
            since = datetime.datetime.fromisoformat(g["active_since"].replace("Z", "+00:00"))
            if now - since > datetime.timedelta(days=1):
                out.append(f"{where}: on fallback {g['active']} since {_when(g['active_since'])} ({_down(g)})")
    return out


def dispvm_escapes(m, snap: Snapshot) -> list[str]:
    """Listed qubes on an egress whose disposables would go out another way:
    a disposable's netvm is its disposable template's, not its opener's. A
    template behind a torii proxy, or with no netvm, is no escape."""
    out = []
    for name, q in sorted(m.qubes.items()):
        info = snap.qubes.get(name)
        if q.egress == "direct" or info is None or not info.default_dispvm:
            continue
        dvm = snap.qubes.get(info.default_dispvm)
        if dvm is None or not dvm.netvm:
            continue
        nv = snap.qubes.get(dvm.netvm)
        if nv is not None and derive.OWNED_TAG in nv.tags:
            continue
        out.append(f"{name} uses egress {q.egress}, but its default_dispvm ({dvm.name}) has netvm {dvm.netvm}: "
                   f"disposables opened from {name} go out directly")
    return out


# charter:realizes SW-261127-SMYC, SW-261127-P8H7
def check_problems(m, snap: Snapshot, ps: dict[str, dict[str, Any]], bundle_version: str, now) -> list[str]:
    """Every difference between the live machine and the file, one line
    each; none when they match. ps is each proxy's config, as its
    features hold it, and status."""
    from . import plan
    problems = []
    try:
        steps = plan.make(m, snap, bundle_version)
    except plan.PlanError as e:
        problems += [f"file: {l}" for l in str(e).splitlines()]
        steps = []
    problems += [f"drift: {s}" for s in steps]
    for p in sorted(m.proxies):
        d = ps.get(p)
        if d is None:
            continue
        if d.get("config_error"):
            problems.append(f"{p}: its config features are unreadable: {d['config_error']}")
        if p in snap.qubes and snap.qubes[p].state != "Running":
            problems.append(f"{p}: not running; its workloads have no network")
            continue
        if d.get("status_error"):
            problems.append(f"{p}: no status: {d['status_error']}")
            continue
        st = (d.get("status") or {}).get("status") or {}
        want = (d.get("config") or {}).get("version")
        if want and st.get("config_version") != want:
            problems.append(f"{p}: runs config {st.get('config_version')}, its features hold {want}")
        for w, ws in sorted((st.get("workloads") or {}).items()):
            ident = ws.get("identity") or {}
            if ident and ident.get("state") != "Running":
                problems.append(f"{w}: identity {ident.get('hostname')} is {ident.get('state')}"
                                + (f"; sign in: {ident['auth_url']}" if ident.get("auth_url") else ""))
            if ident.get("tag_mismatch"):
                problems.append(f"{w}: {ident['tag_mismatch']}")
        problems += egress_problems(p, st, now)
        for line in unlisted_lines(snap, d.get("status") or {}):
            if not line.endswith(", 0 refused"):
                problems.append(f"{p}:{line.strip()} (attached, not in the file)")
    problems += dispvm_escapes(m, snap)
    # Policy torii did not write that widens what it set: only this mesh's
    # own file and the shared deny file are torii's here; another mesh's
    # file is named too, since check cannot tell it from a hand-written one.
    own = {derive.policy_name(m), derive.DENY_POLICY}
    names = set(m.qubes) | set(m.proxies)
    for r in rules(snap, derive.policy_name(m)):
        if r.file in own:
            continue
        if r.include:
            problems.append(f"policy {r.file}: includes {r.target}, which check does not read")
            continue
        if r.action not in ("allow", "ask"):
            continue
        ends = (r.source, r.requested or r.target, r.target)
        widens = r.service in ("torii.Connect", "torii.Status", "*") or \
            (r.service == "qubes.ConnectTCP" and any(x in names or x.startswith("@") for x in ends))
        if widens:
            what = "also allows" if r.action == "allow" else "may allow, on a prompt,"
            note = " (a token check does not evaluate)" if r.tokens else ""
            problems.append(f"policy {r.file}: {what} {r.service} {r.arg} {r.source} -> {r.target}{note}")
    return problems
