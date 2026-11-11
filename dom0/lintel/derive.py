"""From the machine file and a snapshot of the live system, the state the
machine should be in. Pure: no Qubes calls, so plans are golden-tested.
"""

from __future__ import annotations

import base64
import dataclasses
import gzip
import hashlib
import json
from typing import Any

from .machine import Machine, Qube

POLICY_NAME = "50-lintel"  # the default mesh's; a named mesh's is 50-lintel-<mesh>
DENY_POLICY = "59-lintel-deny"  # shared by every mesh, after all of their allows
OWNED_TAG = "lintel-owned"  # qubes the tool created and may delete
MANAGED_TAG = "lintel-managed"  # workloads whose network settings it set; -<mesh> for a named mesh
MESH_TAG = "lintel-mesh-"  # + mesh: on a named mesh's proxy qubes
KEEP_TAG = "lintel-netvm-kept"  # the earlier `network-card: keep`; only cleaned up now
DEFAULT_NETVM = "<default>"  # network: default: Qubes' default netvm, followed as it changes
PROXY_SERVICE = "lintel-proxy"
CLIENT_SERVICE = "lintel-client"
CONFIG_FEATURE = "vm-config.lintel"  # chunks: vm-config.lintel.0, .1, ...
CLIENT_FEATURE = "vm-config.lintel-client"
CHUNK = 2800  # QubesDB refuses values of 3072 bytes or more
STATE_DIR = "/rw/lintel/state"
LOG_DIR = "/rw/lintel/log"
KEY_DIR = "/rw/lintel/keys"  # the sign-in fallback: one-time keys, put there by `lintel sign-in-key`
CLIENT_VERSION_FEATURE = "lintel-client-version"  # on templates that carry the client
# On a proxy qube: the package version apply last started it on. A proxy
# runs what its template had when it started, so a template upgraded by
# another mesh (or by hand) leaves it on the old one until it restarts.
STARTED_VERSION_FEATURE = "lintel-started-version"
# On a workload, written once at its first adoption and never applied: the
# netvm it had before lintel ("none", "<default>" or a name), and the date.
PRIOR_NETVM_FEATURE = "lintel-prior-netvm"
ADOPTED_FEATURE = "lintel-adopted"


@dataclasses.dataclass
class QubeState:
    """The settings the tool owns on one qube."""

    netvm: str | None  # "" for none; None: leave as is
    services: dict[str, bool]
    features: dict[str, str | None]  # None: remove
    managed: bool  # carries MANAGED_TAG


def proxy_config(m: Machine, proxy: str, addresses: dict[str, str], version: str) -> dict[str, Any]:
    """The proxy program's config (proxy/internal/config) for one proxy qube."""
    workloads = {}
    for q in sorted(m.qubes.values(), key=lambda q: q.name):
        if q.proxy != proxy or not q.via_proxy:
            continue
        w: dict[str, Any] = {"link": q.link}
        if q.network_card:
            w["address"] = addresses.get(q.name, "")
        if q.identity != "none":
            ident: dict[str, Any] = {"hostname": q.hostname, "auth_key_file": f"{KEY_DIR}/{q.name}"}
            if q.identity == "login":
                ident["login"] = True
            else:
                ident["tags"] = list(q.tags)
            w["identity"] = ident
        if q.inbound:
            w["inbound"] = _inbound_cfg(q.inbound)
        if q.tailnet_outbound:
            w["tailnet_outbound"] = True
        if q.tailnet_allow:
            w["tailnet_allow"] = list(q.tailnet_allow)
        # The proxy's config says full where the file says all.
        for zone, mode, allow in (("internet", q.internet, q.internet_allow), ("lan", q.lan, q.lan_allow)):
            if mode != "none":
                w[zone] = "full" if mode == "all" else mode
            if allow:
                w[f"{zone}_allow"] = list(allow)
        if q.internet_via != "direct":
            w["exit_node"] = q.internet_via
        if q.forwards:
            w["forwards"] = [{"name": f.name, "to": f.to} for f in q.forwards]
        if q.socks_port:
            w["socks"] = True
        if q.http_port:
            w["http"] = True
        workloads[q.name] = w
    # Services: a workload "<qube>.<service>" each (the name its identity's
    # state is kept under), inbound only, carried to the qube over qrexec.
    for x in sorted(m.services.values(), key=lambda x: x.name):
        if x.proxy != proxy:
            continue
        workloads[f"{x.qube}.{x.name}"] = {
            "link": "qrexec", "qube": x.qube,
            "identity": {"hostname": x.hostname, "tags": list(x.tags),
                         "auth_key_file": f"{KEY_DIR}/{x.qube}.{x.name}"},
            "inbound": _inbound_cfg(x.ports)}
    cfg: dict[str, Any] = {
        "version": version,
        "proxy_qube": proxy,
        "state_dir": STATE_DIR,
        "log_dir": LOG_DIR,
        "workloads": workloads,
    }
    if m.control_url:
        cfg["control_url"] = m.control_url
    return cfg


def _inbound_cfg(inbound) -> list[dict[str, Any]]:
    return [{"port": i.port, **({"target": i.target} if i.target != i.port else {}),
             **({"tls": True} if i.tls else {})} for i in inbound]


def encode_chunks(cfg: dict[str, Any]) -> dict[str, str]:
    """The config as features: gzip, base64, split under QubesDB's limit.

    vm-config.lintel holds "<parts> <sha256 of the JSON>"; the program reads
    vm-config.lintel.0 .. parts-1, joins, decodes, and checks the hash, so a
    half-finished apply is refused rather than half-read.
    """
    raw = json.dumps(cfg, sort_keys=True, separators=(",", ":")).encode()
    b = base64.b64encode(gzip.compress(raw, mtime=0)).decode()
    parts = [b[i:i + CHUNK] for i in range(0, len(b), CHUNK)] or [""]
    out = {CONFIG_FEATURE: f"{len(parts)} {hashlib.sha256(raw).hexdigest()}"}
    for i, p in enumerate(parts):
        out[f"{CONFIG_FEATURE}.{i}"] = p
    return out


def decode_chunks(features: dict[str, str]) -> dict[str, Any] | None:
    head = features.get(CONFIG_FEATURE)
    if not head:
        return None
    n, digest = head.split()
    b = "".join(features.get(f"{CONFIG_FEATURE}.{i}", "") for i in range(int(n)))
    raw = gzip.decompress(base64.b64decode(b))
    if hashlib.sha256(raw).hexdigest() != digest:
        raise ValueError("config chunks do not match their hash")
    return json.loads(raw)


def config_version(m: Machine, addresses: dict[str, str]) -> str:
    """A version that changes exactly when some proxy's config would."""
    blob = json.dumps({p: proxy_config(m, p, addresses, "") for p in m.proxies}, sort_keys=True)
    return hashlib.sha256(blob.encode()).hexdigest()[:12]


def client_config(q: Qube) -> dict[str, Any] | None:
    """The workload-side listeners (lintel-client), or None if it needs none."""
    listen = []
    if q.socks_port:
        listen.append({"port": q.socks_port, "service": "lintel.Connect+socks", "target": "@default"})
    if q.http_port:
        listen.append({"port": q.http_port, "service": "lintel.Connect+http", "target": "@default"})
    for f in q.forwards:
        listen.append({"port": f.port, "service": f"lintel.Connect+f.{f.name}", "target": "@default"})
    return {"listen": listen} if listen else None


def local_client_listens(m: Machine, name: str) -> list[dict[str, Any]]:
    """local-inbound seen from the source: a loopback port into the target qube."""
    out = []
    for q in sorted(m.qubes.values(), key=lambda q: q.name):
        for li in q.local_inbound:
            if name in li.sources:
                out.append({"port": li.port, "service": f"qubes.ConnectTCP+{li.port}", "target": q.name})
    return out


def qube_state(m: Machine, q: Qube) -> QubeState:
    netvm = {"none": "", "proxy": m.proxies[q.proxy].name, "default": DEFAULT_NETVM}.get(q.network, q.network)
    cc = client_config(q) or {"listen": []}
    cc["listen"] = cc["listen"] + local_client_listens(m, q.name)
    has_client = bool(cc["listen"])
    return QubeState(
        netvm=netvm,
        services={CLIENT_SERVICE: has_client},
        features={
            CLIENT_FEATURE: json.dumps(cc, sort_keys=True, separators=(",", ":")) if has_client else None,
        },
        managed=True,
    )


def reset_state() -> QubeState:
    """What a qube leaving the file gets: no netvm (failing closed), and
    nothing of the tool's. Its adoption record stays."""
    return QubeState(netvm="", services={CLIENT_SERVICE: False},
                     features={CLIENT_FEATURE: None}, managed=False)


def policy(m: Machine) -> str:
    """The tool's own qrexec policy file. Every call runs in the direction
    of the connection it carries."""
    lines = [
        "# Written by lintel apply from the machine file. Do not edit: the next",
        "# apply replaces it. Hand-written policy belongs in other files.",
        "",
    ]
    inbound, outbound, local = [], [], []
    for q in sorted(m.qubes.values(), key=lambda q: q.name):
        p = q.proxy
        # Inbound goes over qrexec for routed workloads too (their own
        # firewall drops new connections from their netvm).
        for i in q.inbound:
            inbound.append(f"qubes.ConnectTCP  +{i.target}  {p}  {q.name}  allow")
        if not q.network_card:
            args = (["+socks"] if q.socks_port else []) + (["+http"] if q.http_port else []) \
                + [f"+f.{f.name}" for f in q.forwards]
            for a in args:
                outbound.append(f"lintel.Connect  {a}  {q.name}  @default  allow target={p}")
                outbound.append(f"lintel.Connect  {a}  {q.name}  {p}  allow")
        for li in q.local_inbound:
            for s in li.sources:
                local.append(f"qubes.ConnectTCP  +{li.port}  {s}  {q.name}  allow")
    inbound, outbound, local = (list(dict.fromkeys(x)) for x in (inbound, outbound, local))
    if inbound:
        lines += ["# Inbound: the proxy qube calls into the workload, on listed ports only."] + inbound + [""]
    if outbound:
        lines += ["# Outbound: workloads that declare outbound access call their proxy qube."] + outbound + [""]
    svc = list(dict.fromkeys(f"qubes.ConnectTCP  +{i.target}  {x.proxy}  {x.qube}  allow"
                             for x in sorted(m.services.values(), key=lambda x: x.name) for i in x.ports))
    if svc:
        lines += ["# Services: the proxy qube calls into the qube that hosts them, on their ports only."] + svc + [""]
    if local:
        lines += ["# local-inbound: qubes on this machine, by port."] + local + [""]
    lines += ["# dom0 and the named admin qubes read status and the decision log."]
    for p in sorted(m.proxies):
        lines.append(f"lintel.Status  *  dom0  {p}  allow user=root")
        for r in m.status_readers:
            lines.append(f"lintel.Status  *  {r}  {p}  allow user=root")
    lines += ["", "# Everything else is denied by 59-lintel-deny.policy, after every mesh's file.", ""]
    return "\n".join(lines)


def grants(m: Machine) -> dict[str, Any]:
    """Proposed tag owners and grants, for a person to review and paste.

    Who may reach an inbound port is not in the machine file, so the
    proposal names autogroup:member; narrow it before pasting.
    """
    tags: set[str] = set()
    out: list[dict[str, Any]] = []
    for q in sorted(m.qubes.values(), key=lambda q: q.name):
        tags.update(q.tags)
        if q.inbound and q.identity == "tagged":
            out.append({"src": ["autogroup:member"], "dst": list(q.tags),
                        "ip": [f"tcp:{i.port}" for i in q.inbound]})
        if q.internet_via != "direct":
            out.append({"src": list(q.tags) or ["autogroup:member"], "dst": ["autogroup:internet"], "ip": ["*"]})
    for x in sorted(m.services.values(), key=lambda x: x.name):
        tags.update(x.tags)
        out.append({"src": ["autogroup:member"], "dst": list(x.tags), "ip": [f"tcp:{i.port}" for i in x.ports]})
    return {"tagOwners": {t: list(m.tag_owners) for t in sorted(tags)}, "grants": out}


# --- meshes: one machine may have several files, each its own mesh --------

def policy_name(m: Machine) -> str:
    return f"{POLICY_NAME}-{m.mesh}" if m.mesh else POLICY_NAME


def managed_tag(m: Machine) -> str:
    return f"{MANAGED_TAG}-{m.mesh}" if m.mesh else MANAGED_TAG


def is_managed(tags) -> bool:
    """Managed by some mesh."""
    return any(t == MANAGED_TAG or t.startswith(MANAGED_TAG + "-") for t in tags)


def owned_by(m: Machine, tags) -> bool:
    """One of the tool's own qubes, and this mesh's (the default mesh's
    carry no mesh tag)."""
    if OWNED_TAG not in tags:
        return False
    meshes = {t[len(MESH_TAG):] for t in tags if t.startswith(MESH_TAG)}
    return meshes == ({m.mesh} if m.mesh else set())


def deny_policy() -> str:
    """The catch-all denies, in their own file after every mesh's allows:
    first match wins, so a mesh's deny in its own file would block the
    allows of a mesh whose file sorts after it."""
    return "\n".join([
        "# Written by lintel apply, the same from every mesh. Do not edit.",
        "lintel.Connect  *  @anyvm  @anyvm  deny",
        "lintel.Status   *  @anyvm  @anyvm  deny", ""])
