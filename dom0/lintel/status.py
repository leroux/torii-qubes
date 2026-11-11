"""Reading a proxy qube's status through its read-only lintel.Status service."""

from __future__ import annotations

import json
from typing import Any


class StatusError(Exception):
    pass


def read(vm, timeout: float = 15) -> dict[str, Any]:
    """{"status": ..., "log": [...]} from the running proxy program."""
    try:
        p = vm.run_service("lintel.Status", user="root")
        out, err = p.communicate(b"", timeout=timeout) if _has_timeout(p) else p.communicate(b"")
    except Exception as e:  # noqa: BLE001
        raise StatusError(f"lintel.Status: {e}") from e
    if p.returncode:
        raise StatusError(f"lintel.Status exit {p.returncode}: {(err or b'').decode(errors='replace').strip()}")
    try:
        return json.loads(out)
    except ValueError as e:
        raise StatusError(f"lintel.Status: not JSON: {e}") from e


def _has_timeout(p) -> bool:
    import inspect
    try:
        return "timeout" in inspect.signature(p.communicate).parameters
    except (TypeError, ValueError):
        return False


def sign_in_links(st: dict[str, Any]) -> dict[str, str]:
    """Identities waiting for sign-in: workloads by name, egresses as
    "egress <name>"."""
    out = {}
    s = st.get("status", {})
    for name, w in (s.get("workloads") or {}).items():
        ident = w.get("identity") or {}
        if ident.get("auth_url"):
            out[name] = ident["auth_url"]
    for name, g in (s.get("egresses") or {}).items():
        ident = g.get("identity") or {}
        if ident.get("auth_url"):
            out[f"egress {name}"] = ident["auth_url"]
    return out
