"""A snapshot of the live system: the settings the tool reads and owns.

The snapshot is plain data (JSON-able), so plans are golden-tested from
recorded snapshots, and ``torii snapshot`` records one from a machine.
"""

from __future__ import annotations

import dataclasses
import json
from typing import Any

from . import derive


@dataclasses.dataclass
class QubeInfo:
    name: str
    klass: str
    state: str
    template: str | None
    netvm: str  # "" for none
    provides_network: bool
    qid: int
    ip: str
    autostart: bool
    label: str
    tags: list[str]
    features: dict[str, str]
    netvm_default: bool = False  # its netvm follows Qubes' default
    template_for_dispvms: bool = False  # a disposable template
    default_dispvm: str = ""  # the disposable template its disposables come from

    @property
    def address(self) -> str:
        """Its Qubes IPv4 once routed: the ip property, else Qubes' default."""
        if self.ip:
            return self.ip
        if self.klass == "AppVM" or self.klass == "StandaloneVM":
            return f"10.137.{(self.qid >> 8) & 0xFF}.{self.qid & 0xFF}"
        return ""


@dataclasses.dataclass
class Snapshot:
    qubes: dict[str, QubeInfo]
    policy: str | None  # the tool's policy file, None if absent
    other_policy: dict[str, str]  # hand-written files that mention torii services; read only
    default_netvm: str = ""  # Qubes' global default netvm

    def to_json(self) -> str:
        return json.dumps({
            "qubes": {n: dataclasses.asdict(q) for n, q in sorted(self.qubes.items())},
            "policy": self.policy,
            "other_policy": self.other_policy,
            "default_netvm": self.default_netvm,
        }, indent=1, sort_keys=True)

    @classmethod
    def from_json(cls, text: str) -> "Snapshot":
        d = json.loads(text)
        return cls(qubes={n: QubeInfo(**q) for n, q in d["qubes"].items()},
                   policy=d.get("policy"), other_policy=d.get("other_policy", {}),
                   default_netvm=d.get("default_netvm", ""))


def _prop(vm, name: str, default: Any = None) -> Any:
    try:
        return getattr(vm, name)
    except AttributeError:
        return default
    except Exception:  # qubesadmin raises QubesNoSuchPropertyError and friends
        return default


def _is_default(vm, prop: str) -> bool:
    try:
        return bool(vm.property_is_default(prop))
    except Exception:  # noqa: BLE001
        return False


def take(app, policy_client, interesting: set[str], policy_name: str = derive.POLICY_NAME) -> Snapshot:
    """Read the live system.

    Every qube's class, netvm and tags are read; features only for the
    qubes in ``interesting`` (the file's, the tool's own) and those that
    carry the tool's tags, to keep the call count small.
    """
    qubes = {}
    for vm in app.domains:
        if vm.name == "dom0":
            continue
        tags = sorted(vm.tags)
        want = vm.name in interesting or derive.OWNED_TAG in tags or derive.is_managed(tags)
        netvm = _prop(vm, "netvm")
        tpl = _prop(vm, "template")
        qubes[vm.name] = QubeInfo(
            name=vm.name,
            klass=vm.klass,
            state=vm.get_power_state(),
            template=str(tpl) if tpl else None,
            netvm=str(netvm) if netvm else "",
            provides_network=bool(_prop(vm, "provides_network", False)),
            qid=int(_prop(vm, "qid", 0)),
            ip=str(_prop(vm, "ip", "") or ""),
            autostart=bool(_prop(vm, "autostart", False)),
            label=str(_prop(vm, "label", "")),
            tags=tags,
            netvm_default=_is_default(vm, "netvm"),
            template_for_dispvms=bool(_prop(vm, "template_for_dispvms", False)),
            default_dispvm=str(_prop(vm, "default_dispvm", "") or ""),
            features={k: str(v) for k, v in vm.features.items()} if want else {},
        )
    policy = None
    other: dict[str, str] = {}
    if policy_client is not None:
        files = policy_client.policy_list()
        if policy_name in files:
            policy, _token = policy_client.policy_get(policy_name)
        for f in files:
            if f == policy_name:
                continue
            text, _ = policy_client.policy_get(f)
            other[f] = text  # every file: a wildcard service names no service
    dn = _prop(app, "default_netvm")
    return Snapshot(qubes=qubes, policy=policy, other_policy=other, default_netvm=str(dn) if dn else "")
