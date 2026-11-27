"""The machine file: one per machine, the source of truth for each qube's
connectivity.

A value comes from the qube's entry, else the file's ``defaults``, else the
built-in default. Unknown keys are errors: a typo must not silently widen
or narrow access.
"""

from __future__ import annotations

import dataclasses
import ipaddress
import re
from typing import Any

import yaml

QUBE_RE = re.compile(r"^[A-Za-z][A-Za-z0-9_.-]{0,30}$")
TAG_RE = re.compile(r"^tag:[a-z0-9][a-z0-9-]*$")
SHORT_TAG_RE = re.compile(r"^[a-z0-9][a-z0-9-]*$")
MACHINE_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,20}$")


# charter:realizes SW-261127-P8H7
def tags_of(where: str, raw: Any, machine: str, full_ok: bool) -> tuple[str, ...]:
    """Expand a file's tags. A short tag (`server`) is the machine's:
    tag:<machine>-server. A full one (`tag:chat`) is used as written, and is
    allowed only where full_ok (services: shared tags across machines)."""
    out = []
    for t in raw or []:
        t = str(t)
        if t.startswith("tag:"):
            if not full_ok:
                raise FileError(f"{where}: {t} is a full tag; write the short form ({t[4:]}) "
                                f"for tag:{machine}-{t[4:]}, or use services: for a shared tag")
            if not TAG_RE.match(t):
                raise FileError(f"{where}: {t!r} is not tag:<name>")
            out.append(t)
        elif SHORT_TAG_RE.match(t):
            out.append(f"tag:{machine}-{t}")
        else:
            raise FileError(f"{where}: {t!r} is not a tag (lowercase letters, digits and -)")
    return tuple(dict.fromkeys(out))
FORWARD_RE = re.compile(r"^[A-Za-z0-9_-]{1,40}$")
HOST_RE = re.compile(r"^(\*\.)?[a-z0-9_]([a-z0-9_-]{0,62})(\.[a-z0-9_]([a-z0-9_-]{0,62}))*$")

DEFAULT_PROXY = "sys-torii"
DEFAULT_BASE_TEMPLATE = "fedora-44-minimal"
# The tool's template is named after its base, so a new base is a new
# template: plan creates it, moves the proxies onto it and removes the old.
TEMPLATE_PREFIX = "torii-"
DEFAULT_PROXY_NETVM = "sys-firewall"
DEFAULT_SOCKS_PORT = 1080
DEFAULT_HTTP_PORT = 3128


class FileError(ValueError):
    """The machine file is not valid; the message says where."""


@dataclasses.dataclass(frozen=True)
class Inbound:
    port: int  # on the identity
    target: int  # in the qube
    tls: bool = False  # the identity terminates HTTPS with its own certificate


@dataclasses.dataclass(frozen=True)
class Service:
    """A named endpoint on the tailnet: its own device (today; a Tailscale
    Service later), reached on its ports, carried to a qube's ports over
    qrexec. Any qube may host services, whatever its network."""

    name: str
    qube: str
    proxy: str
    hostname: str
    tags: tuple[str, ...]
    ports: tuple[Inbound, ...]


@dataclasses.dataclass(frozen=True)
class Forward:
    name: str
    to: str  # host:port, dialled by the proxy
    port: int  # the loopback port in the workload


@dataclasses.dataclass(frozen=True)
class LocalInbound:
    port: int
    sources: tuple[str, ...]


@dataclasses.dataclass(frozen=True)
class Qube:
    """One qube's connectivity, with every default resolved."""

    name: str
    network: str  # none | proxy | default | the name of a netvm
    internet: str  # outbound.internet: none | all | allowlist
    internet_allow: tuple[str, ...]
    egress: str  # "direct" or the name of an egress
    identity: str  # none | tagged | login
    proxy: str  # the proxy qube that carries its traffic
    hostname: str
    tags: tuple[str, ...]
    inbound: tuple[Inbound, ...]
    tailnet_outbound: bool
    forwards: tuple[Forward, ...]
    socks_port: int  # 0: none
    http_port: int  # 0: none
    local_inbound: tuple[LocalInbound, ...]
    lan: str = "none"  # outbound.lan: none | all | allowlist
    lan_allow: tuple[str, ...] = ()
    tailnet_allow: tuple[str, ...] = ()  # outbound.tailnet as a list: only these devices
    adopt_force: bool = False  # adopt: force: may move it off a netvm torii doesn't know
    disposables: bool = False  # disposables: yes: it is a disposable template, and that is meant

    @property
    def network_card(self) -> bool:
        """Routed through its proxy qube (the proxy is its netvm)."""
        return self.network == "proxy"

    @property
    def behind_proxy(self) -> bool:
        """The proxy can carry its traffic: no network card, or routed."""
        return self.network in ("none", "proxy")

    @property
    def link(self) -> str:
        return "routed" if self.network_card else "qrexec"

    @property
    def has_outbound(self) -> bool:
        return self.tailnet_outbound or self.internet != "none" or self.lan != "none" or bool(self.forwards)

    @property
    def via_proxy(self) -> bool:
        """Whether a proxy qube carries any of this qube's traffic."""
        return self.network_card or self.identity != "none" or self.has_outbound


@dataclasses.dataclass(frozen=True)
class Unlisted:
    """What qubes attached to a proxy but not in the file get."""

    internet: str  # all | allowlist
    internet_allow: tuple[str, ...]
    egress: str  # "direct" or an egress


@dataclasses.dataclass(frozen=True)
class Proxy:
    name: str
    netvm: str
    label: str
    unlisted: Unlisted | None = None  # None: refused


@dataclasses.dataclass(frozen=True)
class Egress:
    """A way out through exit nodes, tried in order. Each proxy that
    carries it holds one internet-only identity for it."""

    name: str
    exits: tuple[str, ...]
    failback_after: int  # seconds
    tags: tuple[str, ...]
    hostname: str  # on the main proxy; others append -<proxy>
    probe: str  # host:port or ""

    def hostname_on(self, proxy: str, main: str) -> str:
        return self.hostname if proxy == main else f"{self.hostname}-{proxy}"


@dataclasses.dataclass(frozen=True)
class Machine:
    template: str
    base_template: str
    control_url: str
    tag_owners: tuple[str, ...]
    proxies: dict[str, Proxy]
    default_proxy: str
    status_readers: tuple[str, ...]
    qubes: dict[str, Qube]
    defaults: dict[str, Any]
    services: dict[str, Service] = dataclasses.field(default_factory=dict)
    egresses: dict[str, Egress] = dataclasses.field(default_factory=dict)
    name: str = ""  # the machine: prefixes its tags and its qubes' device names
    mesh: str = ""  # this file's mesh; "" is the default. Meshes never touch each other's qubes
    tailscale_logs: bool = False  # tsnet may upload client logs to Tailscale (off: nothing goes direct)

    @property
    def machine_tag(self) -> str:
        return f"tag:{self.name}"

    def egress_instances(self) -> list[tuple[str, str]]:
        """(egress, proxy) for every egress some proxy carries: used by a
        qube there, or by that proxy's unlisted."""
        out = {(q.egress, q.proxy) for q in self.qubes.values() if q.egress != "direct"}
        out |= {(p.unlisted.egress, p.name) for p in self.proxies.values()
                if p.unlisted and p.unlisted.egress != "direct"}
        return sorted(out)


def _check_keys(where: str, d: dict, allowed: set[str]) -> None:
    if not isinstance(d, dict):
        raise FileError(f"{where}: expected a mapping")
    extra = sorted(set(d) - allowed)
    if extra:
        raise FileError(f"{where}: unknown key {extra[0]!r} (known: {', '.join(sorted(allowed))})")


def _bool(where: str, v: Any) -> bool:
    # YAML 1.1 reads yes/no as booleans already; accept the words too.
    if isinstance(v, bool):
        return v
    if v in ("yes", "on", "true"):
        return True
    if v in ("no", "off", "false"):
        return False
    raise FileError(f"{where}: expected yes or no, not {v!r}")


def _port(where: str, v: Any) -> int:
    if isinstance(v, bool) or not isinstance(v, int) or not 0 < v < 65536:
        raise FileError(f"{where}: {v!r} is not a port")
    return v


def _qube_name(where: str, v: Any) -> str:
    if not isinstance(v, str) or not QUBE_RE.match(v):
        raise FileError(f"{where}: {v!r} is not a qube name")
    return v


QUBE_KEYS = {"network", "proxy", "egress", "outbound", "tailnet", "forwards", "socks", "http-proxy", "local-inbound",
             "adopt", "disposables"}
ENTRY_ONLY_KEYS = {"forwards", "local-inbound", "adopt", "disposables"}  # not in defaults
TAILNET_KEYS = {"identity", "hostname", "tags", "inbound"}
OUTBOUND_KEYS = {"internet", "lan", "tailnet"}

# Keys of the earlier schema, and where they went.
MOVED = {
    "internet": "outbound.internet (none, all, or a list)",
    "internet-allow": "the list in outbound.internet",
    "internet-via": "egress: (an egress defined at the top of the file)",
    "network-card": "network: none (was no), proxy (was yes); a qube that kept its own netvm "
                    "is not listed, or has network: default or the netvm's name",
    "identities": "services: at the top of the file, each with qube: <this qube>",
}


def _zone(where: str, v: Any) -> tuple[str, tuple[str, ...]]:
    """A zone's access: none, all, or a list of entries."""
    if v in (None, "none", False, "no"):
        return "none", ()
    if v in ("all", True, "yes"):
        return "all", ()
    # Only none and all are words; one entry is still a list, [pypi.org],
    # so a typo such as `ful` is an error, not a hostname.
    if not isinstance(v, list) or not v:
        raise FileError(f"{where}: none, all, or a list of names, addresses or ranges (each with an optional :port)")
    return "allowlist", tuple(allow_entry(f"{where}[{i}]", e) for i, e in enumerate(v))


def allow_entry(where: str, e: Any) -> str:
    """Check one entry the way the proxy reads it (config.ParseAllow):
    host, *.host, address or range, with an optional :port, then an
    optional /tcp or /udp (none: TCP only)."""
    s = str(e).strip().lower().rstrip(".")
    full = s
    for proto in ("/tcp", "/udp"):
        if s.endswith(proto):
            s = s[: -len(proto)]
    body, port = s, None
    if s.startswith("["):
        if "]" not in s:
            raise FileError(f"{where}: {e!r}: unclosed [")
        body, rest = s[1:].split("]", 1)
        if rest:
            if not rest.startswith(":"):
                raise FileError(f"{where}: {e!r}")
            port = rest[1:]
    elif s.count(":") == 1:
        body, port = s.split(":")
    if port is not None and not (port.isdigit() and 0 < int(port) < 65536):
        raise FileError(f"{where}: {e!r}: bad port")
    try:
        ipaddress.ip_network(body, strict=False)
        return full
    except ValueError:
        pass
    if not HOST_RE.match(body):
        raise FileError(f"{where}: {e!r} is not a host, *.host, address or range")
    return full


def _merged(entry: dict, defaults: dict, key: str, builtin: Any) -> Any:
    if key in entry:
        return entry[key]
    if key in defaults:
        return defaults[key]
    return builtin


def _inbound(where: str, items) -> list[Inbound]:
    out = []
    seen = set()
    for i, v in enumerate(items or []):
        w = f"{where}[{i}]"
        tls = False
        if isinstance(v, dict):
            _check_keys(w, v, {"port", "target", "tls"})
            p, t = _port(w, v.get("port")), _port(w, v.get("target", v.get("port")))
            tls = _bool(w + ".tls", v.get("tls", False))
        else:
            p = t = _port(w, v)
        if p in seen:
            raise FileError(f"{w}: port {p} twice")
        seen.add(p)
        out.append(Inbound(p, t, tls))
    return out


def _moved(where: str, d: dict) -> None:
    """Point a key of the earlier schema at where it went."""
    if not isinstance(d, dict):
        return
    for k, to in MOVED.items():
        if k in d:
            raise FileError(f"{where}.{k}: moved to {to}")
    if isinstance(d.get("tailnet"), dict) and "outbound" in d["tailnet"]:
        raise FileError(f"{where}.tailnet.outbound: moved to outbound.tailnet")
    if isinstance(d.get("outbound"), dict) and "via" in d["outbound"]:
        raise FileError(f"{where}.outbound.via: removed; define the exit nodes once, "
                        f"`egress: {{<name>: {{exits: [{d['outbound']['via']}]}}}}` at the top of the file, "
                        "and write `egress: <name>` here")


def _resolve(name: str, entry: dict, defaults: dict, default_proxy: str,
             proxies: dict[str, Proxy], where: str, machine: str, egresses: dict | None = None) -> Qube:
    _moved(where, entry)
    _check_keys(where, entry, QUBE_KEYS)
    tn = entry.get("tailnet") or {}
    dtn = defaults.get("tailnet") or {}
    _check_keys(f"{where}.tailnet", tn, TAILNET_KEYS)

    if "network" not in entry and "network" not in defaults:
        raise FileError(f"{where}.network: required (here or in defaults): none, proxy, default or a netvm")
    network = str(_merged(entry, defaults, "network", "none"))
    proxy = str(_merged(entry, defaults, "proxy", default_proxy))
    if proxy not in proxies:
        raise FileError(f"{where}.proxy: no proxy {proxy!r} (the main proxy or one in proxies)")
    if network.startswith("proxy:"):
        raise FileError(f"{where}.network: write proxy: {network.split(':', 1)[1]} and network: proxy")
    if network not in ("none", "proxy", "default"):
        _qube_name(f"{where}.network", network)
        if network in proxies:
            raise FileError(f"{where}.network: write network: proxy and proxy: {network}, not the proxy's name")
    network_card = network == "proxy"
    behind = network in ("none", "proxy")

    ob = entry.get("outbound") or {}
    dob = defaults.get("outbound") or {}
    _check_keys(f"{where}.outbound", ob, OUTBOUND_KEYS)
    internet, allow_n = _zone(f"{where}.outbound.internet", _merged(ob, dob, "internet", "none"))
    lan, lan_n = _zone(f"{where}.outbound.lan", _merged(ob, dob, "lan", "none"))
    egress = str(_merged(entry, defaults, "egress", "direct"))
    if egress != "direct" and egresses is not None and egress not in egresses:
        raise FileError(f"{where}.egress: no egress {egress!r} (define it under egress: at the top of the file)")
    ident = str(_merged(tn, dtn, "identity", "none"))
    if ident.startswith("proxy:"):
        raise FileError(f"{where}.tailnet.identity: write proxy: {ident.split(':', 1)[1]} and identity: tagged")
    if ident == "proxy":
        raise FileError(f"{where}.tailnet.identity: write identity: tagged (was proxy)")
    if ident not in ("none", "tagged", "login"):
        raise FileError(f"{where}.tailnet.identity: tagged, login or none, not {ident!r}")
    hostname = str(tn.get("hostname", f"{machine}-{name}"))
    tags = tags_of(f"{where}.tailnet.tags", _merged(tn, dtn, "tags", []), machine, False)
    if ident == "tagged" and not tags:
        raise FileError(f"{where}: a tagged identity needs tags (here or in defaults.tailnet.tags)")
    if ident != "tagged":
        tags = ()
    else:
        tags = tags + (f"tag:{machine}",)  # every tagged device carries the machine's tag

    inbound = _inbound(f"{where}.tailnet.inbound", tn.get("inbound", []))
    tv = _merged(ob, dob, "tailnet", False)
    tailnet_allow: tuple[str, ...] = ()
    if isinstance(tv, list):
        # A list: only these devices (a name stands for the device, all its
        # names and addresses), addresses or ranges, each with an optional
        # :port. The grants still decide on top.
        if not tv:
            raise FileError(f"{where}.outbound.tailnet: yes, no, or a list of devices")
        tailnet_allow = tuple(allow_entry(f"{where}.outbound.tailnet[{i}]", e) for i, e in enumerate(tv))
        outbound = True
    else:
        outbound = _bool(f"{where}.outbound.tailnet", tv)
    if ident == "none" and (inbound or outbound):
        raise FileError(f"{where}: tailnet inbound and outbound.tailnet need an identity")
    if egress != "direct" and internet == "none":
        raise FileError(f"{where}.egress: an egress carries internet traffic; this qube has outbound.internet: none")

    forwards = []
    fports = set()
    fw = entry.get("forwards", {}) or {}
    if not isinstance(fw, dict):
        raise FileError(f"{where}.forwards: expected name: {{to: host:port, port: N}}")
    for fname, spec in fw.items():
        w = f"{where}.forwards.{fname}"
        if not FORWARD_RE.match(str(fname)):
            raise FileError(f"{w}: bad forward name")
        if isinstance(spec, str):
            spec = {"to": spec}
        _check_keys(w, spec, {"to", "port"})
        to = spec.get("to")
        if not isinstance(to, str) or ":" not in to:
            raise FileError(f"{w}.to: expected host:port")
        _port(w + ".to", int(to.rsplit(":", 1)[1]) if to.rsplit(":", 1)[1].isdigit() else -1)
        p = _port(w + ".port", spec.get("port", int(to.rsplit(":", 1)[1])))
        if p in fports:
            raise FileError(f"{w}.port: {p} used twice")
        fports.add(p)
        forwards.append(Forward(str(fname), to, p))

    if network_card and forwards:
        raise FileError(f"{where}: forwards are for qubes without a network card")
    has_out = outbound or internet != "none" or lan != "none"
    socks = entry.get("socks", defaults.get("socks", DEFAULT_SOCKS_PORT if has_out else 0))
    http = entry.get("http-proxy", defaults.get("http-proxy", DEFAULT_HTTP_PORT if has_out else 0))
    socks = 0 if socks in (False, 0, "no") or network_card or not has_out else _port(where + ".socks", socks)
    http = 0 if http in (False, 0, "no") or network_card or not has_out else _port(where + ".http-proxy", http)
    for p in (socks, http):
        if p and p in fports:
            raise FileError(f"{where}: port {p} is both a forward and a proxy port")

    local = []
    for i, v in enumerate(entry.get("local-inbound", []) or []):
        w = f"{where}.local-inbound[{i}]"
        _check_keys(w, v, {"port", "from"})
        srcs = v.get("from") or []
        if not srcs:
            raise FileError(f"{w}.from: name at least one qube")
        local.append(LocalInbound(_port(w + ".port", v.get("port")),
                                  tuple(_qube_name(w + ".from", s) for s in srcs)))

    if not behind and (ident != "none" or has_out or forwards or inbound or egress != "direct"
                       or entry.get("socks") or entry.get("http-proxy")):
        raise FileError(f"{where}: network: {network} is not carried by the proxy, so no tailnet identity, "
                        "outbound or forwards; reach it from the tailnet with services:")

    adopt = entry.get("adopt")
    if adopt not in (None, "force"):
        raise FileError(f"{where}.adopt: adopt: force, or leave it out")
    disposables = _bool(f"{where}.disposables", entry.get("disposables", False))

    return Qube(name=name, network=network, internet=internet,
                internet_allow=tuple(allow_n), egress=egress, lan=lan, lan_allow=tuple(lan_n), tailnet_allow=tailnet_allow, identity=ident, proxy=proxy,
                hostname=hostname, tags=tuple(str(t) for t in tags), inbound=tuple(inbound),
                tailnet_outbound=outbound, forwards=tuple(forwards), socks_port=socks,
                http_port=http, local_inbound=tuple(local),
                adopt_force=adopt == "force", disposables=disposables)


TOP_KEYS = {"mesh", "machine", "manage", "proxy", "proxies", "egress", "template", "base-template", "tailnet",
            "defaults", "qubes", "services", "status-readers"}
SERVICE_KEYS = {"qube", "proxy", "hostname", "tags", "ports"}


EGRESS_KEYS = {"exits", "failback-after", "tags", "hostname", "probe"}
DURATION_RE = re.compile(r"^(\d+)(s|m|h)?$")


def _duration(where: str, v: Any) -> int:
    """Seconds, from 90, "90s", "2m" or "1h"."""
    m = DURATION_RE.match(str(v)) if not isinstance(v, bool) else None
    if not m:
        raise FileError(f"{where}: a duration such as 90s, 2m or 1h, not {v!r}")
    return int(m.group(1)) * {"s": 1, None: 1, "m": 60, "h": 3600}[m.group(2)]


def _egress(name: str, spec: Any, machine: str) -> Egress:
    where = f"egress.{name}"
    if not QUBE_RE.match(name) or name == "direct":
        raise FileError(f"{where}: bad egress name")
    if not isinstance(spec, dict):
        raise FileError(f"{where}: expected {{exits: [exit nodes]}}")
    _check_keys(where, spec, EGRESS_KEYS)
    exits = spec.get("exits")
    if not isinstance(exits, list) or not exits:
        raise FileError(f"{where}.exits: one or more exit nodes, by tailnet name or address, in order")
    for i, e in enumerate(exits):
        if not isinstance(e, str) or not e or any(c.isspace() for c in e):
            raise FileError(f"{where}.exits[{i}]: {e!r} is not a tailnet name or address")
    if len(set(exits)) != len(exits):
        raise FileError(f"{where}.exits: an exit node twice")
    probe = str(spec.get("probe", ""))
    if probe:
        allow_entry(f"{where}.probe", probe)
        if ":" not in probe.rsplit("]", 1)[-1]:
            raise FileError(f"{where}.probe: host:port")
    tags = tags_of(f"{where}.tags", spec.get("tags", ["egress"]), machine, False) + (f"tag:{machine}",)
    return Egress(name=name, exits=tuple(exits), failback_after=_duration(f"{where}.failback-after", spec.get("failback-after", "2m")),
                  tags=tuple(dict.fromkeys(tags)), hostname=str(spec.get("hostname", f"{machine}-egress-{name}")), probe=probe)


def _unlisted(where: str, v: Any, egresses: dict[str, Egress]) -> Unlisted | None:
    """refuse, or {internet, egress}: what qubes attached to the proxy but
    not in the file get."""
    if v in ("refuse", None):
        return None
    if not isinstance(v, dict):
        raise FileError(f"{where}: refuse, or {{internet: ..., egress: ...}}")
    _check_keys(where, v, {"internet", "egress"})
    internet, allow = _zone(f"{where}.internet", v.get("internet", "none"))
    if internet == "none":
        raise FileError(f"{where}.internet: none is `unlisted: refuse`")
    egress = str(v.get("egress", "direct"))
    if egress != "direct" and egress not in egresses:
        raise FileError(f"{where}.egress: no egress {egress!r}")
    return Unlisted(internet=internet, internet_allow=allow, egress=egress)


def _service(name: str, spec: Any, defaults: dict, default_proxy: str, proxies: dict[str, Proxy],
             machine: str) -> Service:
    where = f"services.{name}"
    if not FORWARD_RE.match(str(name)):
        raise FileError(f"{where}: a service name is letters, digits, - and _")
    _check_keys(where, spec or {}, SERVICE_KEYS)
    spec = spec or {}
    qube = _qube_name(where + ".qube", spec.get("qube"))
    if len(f"{qube}.{name}") > 31:
        raise FileError(f"{where}: with its qube's name, {qube}.{name}, longer than 31 characters")
    proxy = str(spec.get("proxy", default_proxy))
    if proxy not in proxies:
        raise FileError(f"{where}.proxy: no proxy {proxy!r} in proxies")
    tags = tags_of(where + ".tags", spec.get("tags") or (defaults.get("tailnet") or {}).get("tags"), machine, True)
    if not tags:
        raise FileError(f"{where}: needs tags (here or in defaults.tailnet.tags)")
    tags = tuple(dict.fromkeys(tags + (f"tag:{machine}",)))
    ports = _inbound(where + ".ports", spec.get("ports"))
    if not ports:
        raise FileError(f"{where}: name its ports")
    return Service(str(name), qube, proxy, str(spec.get("hostname", name)), tags, tuple(ports))


# charter:realizes SW-261127-9BNW
def parse(text: str) -> Machine:
    try:
        doc = yaml.safe_load(text) or {}
    except yaml.YAMLError as e:
        raise FileError(f"not YAML: {e}") from None
    _check_keys("file", doc, TOP_KEYS)
    mname = doc.get("machine")
    if not isinstance(mname, str) or not MACHINE_RE.match(mname):
        raise FileError("machine: required, this machine's name (lowercase letters, digits and -, "
                        "up to 21): it prefixes its tags (tag:<machine>-server) and its qubes' device names")
    mesh = doc.get("mesh", "")
    if mesh != "" and (not isinstance(mesh, str) or not MACHINE_RE.match(mesh)):
        raise FileError("mesh: a name (lowercase letters, digits and -), for a file applied separately")
    if "manage" in doc:
        raise FileError("manage: removed; torii only changes the qubes listed under qubes:")

    egresses = {}
    raw_egress = doc.get("egress", {}) or {}
    if not isinstance(raw_egress, dict):
        raise FileError("egress: expected name: {exits: [exit nodes]}")
    for ename, spec in raw_egress.items():
        egresses[str(ename)] = _egress(str(ename), spec, mname)

    # proxy: NAME or {name, netvm, label, unlisted}; proxies: more, dedicated.
    def proxy_of(where, v):
        if isinstance(v, str):
            v = {"name": v}
        _check_keys(where, v, {"name", "netvm", "label", "unlisted"})
        return Proxy(_qube_name(where + ".name", v.get("name", DEFAULT_PROXY)),
                     _qube_name(where + ".netvm", v.get("netvm", DEFAULT_PROXY_NETVM)),
                     str(v.get("label", "orange")),
                     _unlisted(where + ".unlisted", v.get("unlisted", "refuse"), egresses))

    main = proxy_of("proxy", doc.get("proxy", DEFAULT_PROXY))
    proxies = {main.name: main}
    for i, v in enumerate(doc.get("proxies", []) or []):
        p = proxy_of(f"proxies[{i}]", v)
        if p.name in proxies:
            raise FileError(f"proxies[{i}]: {p.name} twice")
        proxies[p.name] = p

    tnet = doc.get("tailnet", {}) or {}
    _check_keys("tailnet", tnet, {"control-url", "tag-owners", "tailscale-logs"})
    defaults = doc.get("defaults", {}) or {}
    _moved("defaults", defaults)
    _check_keys("defaults", defaults, QUBE_KEYS - ENTRY_ONLY_KEYS)
    if "tailnet" in defaults:
        _check_keys("defaults.tailnet", defaults["tailnet"], {"identity", "tags"})
    if "outbound" in defaults:
        _check_keys("defaults.outbound", defaults["outbound"], OUTBOUND_KEYS)

    qubes = {}
    for name, entry in (doc.get("qubes", {}) or {}).items():
        _qube_name("qubes", name)
        if name in proxies:
            raise FileError(f"qubes.{name}: is a proxy qube; the tool owns it")
        qubes[name] = _resolve(name, entry or {}, defaults, main.name, proxies, f"qubes.{name}", mname, egresses)

    services = {}
    raw_services = doc.get("services", {}) or {}
    if not isinstance(raw_services, dict):
        raise FileError("services: expected name: {qube, tags, ports}")
    for sname, spec in raw_services.items():
        services[str(sname)] = _service(sname, spec, defaults, main.name, proxies, mname)

    hostnames = {}
    owners = [(f"qubes.{q.name}", q.hostname) for q in qubes.values() if q.identity != "none"] + \
             [(f"services.{x.name}", x.hostname) for x in services.values()]
    m0 = Machine(template="", base_template="", control_url="", tag_owners=(), proxies=proxies,
                 default_proxy=main.name, status_readers=(), qubes=qubes, defaults={}, egresses=egresses)
    owners += [(f"egress.{g}", egresses[g].hostname_on(p, main.name)) for g, p in m0.egress_instances()]
    for where, h in owners:
        h = h.lower()
        if h in hostnames:
            raise FileError(f"{where}: tailnet name {h} also used by {hostnames[h]}")
        hostnames[h] = where
    for q in qubes.values():
        for li in q.local_inbound:
            for s in li.sources:
                if s == q.name:
                    raise FileError(f"qubes.{q.name}.local-inbound: a qube is not its own source")

    base = _qube_name("base-template", doc.get("base-template", DEFAULT_BASE_TEMPLATE))
    m = Machine(template=_qube_name("template", doc.get("template", TEMPLATE_PREFIX + base)),
                base_template=base,
                control_url=str(tnet.get("control-url", "")),
                tag_owners=tuple(tnet.get("tag-owners", ["autogroup:admin"])),
                proxies=proxies, default_proxy=main.name,
                status_readers=tuple(_qube_name("status-readers", s) for s in doc.get("status-readers", []) or []),
                qubes=qubes, defaults=defaults, services=services, egresses=egresses, name=mname, mesh=mesh,
                tailscale_logs=_bool("tailnet.tailscale-logs", tnet.get("tailscale-logs", False)))
    # The defaults themselves must resolve (network may come from entries).
    _resolve("defaults-check", {} if "network" in defaults else {"network": "none"}, defaults, main.name,
             proxies, "defaults", mname, egresses)
    return m


def load(path: str) -> Machine:
    with open(path, encoding="utf-8") as f:
        return parse(f.read())
