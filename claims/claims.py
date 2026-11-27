# Code generated from claims by charter gen. DO NOT EDIT.
"""The charter's record as Python: a symbol per entry and per observation,
and the verifies and observes a test opens with. With CHARTER_EVENTS set,
each citation and its test's outcome is appended to that file as a JSON line
once the test ends. Under pytest, list this module in pytest_plugins so its
hook sees each test's outcome."""

import json
import os
import sys
import threading

__all__ = ["Spec", "Observation", "verifies", "observes"]


class Spec:
    """An entry of the record: a need or a decision."""

    __slots__ = ("id", "title", "status", "kind")

    def __init__(self, id, title, status, kind):
        object.__setattr__(self, "id", id)
        object.__setattr__(self, "title", title)
        object.__setattr__(self, "status", status)
        object.__setattr__(self, "kind", kind)

    def __setattr__(self, name, value):
        raise AttributeError("an entry is changed in its document, not by a test")


class Observation:
    """A fact about a system the code does not own."""

    __slots__ = ("name", "subject", "status")

    def __init__(self, name, subject, status):
        object.__setattr__(self, "name", name)
        object.__setattr__(self, "subject", subject)
        object.__setattr__(self, "status", status)

    def __setattr__(self, name, value):
        raise AttributeError("an observation is changed in its document, not by a test")


_lock = threading.Lock()
_pending = {}  # pytest node id -> [(id, handle, file)]


def _write(id, handles, test, file, outcome):
    path = os.environ.get("CHARTER_EVENTS", "")
    if not path:
        return
    with _lock, open(path, "a", encoding="utf-8") as f:
        for h in handles or [""]:
            line = {"id": id, "test": test, "file": file, "outcome": outcome}
            if h:
                line["handle"] = h
            # One write per line, opened to append: a line is never split by
            # another process's.
            f.write(json.dumps(line) + "\n")


def _unittest_outcome(tc):
    outcome = getattr(tc, "_outcome", None)
    result = getattr(outcome, "result", None)
    if result is not None:
        if any(t is tc for t, _ in getattr(result, "skipped", [])):
            return "skipped"
        if any(t is tc for t, _ in list(result.failures) + list(result.errors)):
            return "failed"
    if outcome is not None and not getattr(outcome, "success", True):
        return "failed"
    return "passed"


def _record(tc, id, handles):
    file = sys._getframe(2).f_code.co_filename
    if not os.environ.get("CHARTER_EVENTS", ""):
        return
    if tc is None:
        node = os.environ.get("PYTEST_CURRENT_TEST", "").rsplit(" ", 1)[0]
        _pending.setdefault(node, []).append((id, handles, file))
        return
    tc.addCleanup(lambda: _write(id, handles, tc.id(), file, _unittest_outcome(tc)))


def verifies(tc, spec, *handles):
    """Opens a test that verifies an entry, naming by handle the scenarios it
    exercises: tc is the unittest.TestCase, or None under pytest."""
    if not isinstance(spec, Spec):
        raise TypeError("claims: verifies needs a spec")
    _record(tc, spec.id, list(handles))


def observes(tc, observation):
    """Opens a probe that checks an observation against the live system."""
    if not isinstance(observation, Observation):
        raise TypeError("claims: observes needs an observation")
    _record(tc, observation.name, [])


def pytest_runtest_logreport(report):
    """The pytest hook: a test's call report, or a failed or skipped setup,
    gives the outcome of the citations it made."""
    if report.when == "call" or (report.when == "setup" and not report.passed):
        outcome = "passed" if report.passed else "skipped" if report.skipped else "failed"
        for id, handles, file in _pending.pop(report.nodeid, []):
            _write(id, handles, report.nodeid, file, outcome)

#: STK_261126_346B is STK-261126-346B: A qube on an egress reaches the internet only through that egress's exit nodes, or not at all
STK_261126_346B = Spec("STK-261126-346B", "A qube on an egress reaches the internet only through that egress's exit nodes, or not at all", "planned", "")

#: STK_261126_4Q75 is STK-261126-4Q75: show reads each qube's reach from the live machine; check names every difference from the file
STK_261126_4Q75 = Spec("STK-261126-4Q75", "show reads each qube's reach from the live machine; check names every difference from the file", "planned", "")

#: STK_261126_B4CP is STK-261126-B4CP: apply changes only what torii owns, and exactly the steps plan showed
STK_261126_B4CP = Spec("STK-261126-B4CP", "apply changes only what torii owns, and exactly the steps plan showed", "planned", "")

#: STK_261126_KHZB is STK-261126-KHZB: Each tailnet identity is one qube's, joins only by a person's approval, and dom0 holds no tailnet credential
STK_261126_KHZB = Spec("STK-261126-KHZB", "Each tailnet identity is one qube's, joins only by a person's approval, and dom0 holds no tailnet credential", "planned", "")

#: STK_261126_KZK8 is STK-261126-KZK8: A carried qube's connection is relayed only if its entry allows it; every other connection is refused
STK_261126_KZK8 = Spec("STK-261126-KZK8", "A carried qube's connection is relayed only if its entry allows it; every other connection is refused", "planned", "")

#: STK_261126_SM33 is STK-261126-SM33: torii adds no qrexec call, network path or dom0 exposure beyond those the file declares
STK_261126_SM33 = Spec("STK-261126-SM33", "torii adds no qrexec call, network path or dom0 exposure beyond those the file declares", "planned", "")

#: SW_261127_2JH3 is SW-261127-2JH3: plan refuses before any step a file that names what this machine cannot be given
SW_261127_2JH3 = Spec("SW-261127-2JH3", "plan refuses before any step a file that names what this machine cannot be given", "active", "invariant")

#: SW_261127_2R9T is SW-261127-2R9T: A name entry counts only for a name the qube asked for, or resolved through the proxy to that address
SW_261127_2R9T = Spec("SW-261127-2R9T", "A name entry counts only for a name the qube asked for, or resolved through the proxy to that address", "active", "invariant")

#: SW_261127_2XS6 is SW-261127-2XS6: Allow entries match by exact name or subdomain, port and protocol, TCP unless they say UDP
SW_261127_2XS6 = Spec("SW-261127-2XS6", "Allow entries match by exact name or subdomain, port and protocol, TCP unless they say UDP", "active", "invariant")

#: SW_261127_3Q23 is SW-261127-3Q23: Every proxy qube has IPv6 off, for itself and every qube behind it
SW_261127_3Q23 = Spec("SW-261127-3Q23", "Every proxy qube has IPv6 off, for itself and every qube behind it", "active", "invariant")

#: SW_261127_63CS is SW-261127-63CS: apply makes the plan's steps in order, stops at the first that fails, and a second run continues
SW_261127_63CS = Spec("SW-261127-63CS", "apply makes the plan's steps in order, stops at the first that fails, and a second run continues", "active", "invariant")

#: SW_261127_6S2F is SW-261127-6S2F: Under an allowlist, a TLS server name must be allowed too, and nothing is relayed before it is read
SW_261127_6S2F = Spec("SW-261127-6S2F", "Under an allowlist, a TLS server name must be allowed too, and nothing is relayed before it is read", "active", "invariant")

#: SW_261127_7C3Y is SW-261127-7C3Y: A qrexec caller is the qube dom0 names, never one the caller states
SW_261127_7C3Y = Spec("SW-261127-7C3Y", "A qrexec caller is the qube dom0 names, never one the caller states", "active", "invariant")

#: SW_261127_9BNW is SW-261127-9BNW: The machine file is read whole and refused whole when a key, a network or a value is wrong
SW_261127_9BNW = Spec("SW-261127-9BNW", "The machine file is read whole and refused whole when a key, a network or a value is wrong", "active", "invariant")

#: SW_261127_B6Z6 is SW-261127-B6Z6: A routed qube's traffic reaches the program and nothing else: interception, with forwarding off
SW_261127_B6Z6 = Spec("SW-261127-B6Z6", "A routed qube's traffic reaches the program and nothing else: interception, with forwarding off", "active", "invariant")

#: SW_261127_BHHX is SW-261127-BHHX: A routed qube is named by its Qubes source address, which Qubes keeps it from forging
SW_261127_BHHX = Spec("SW-261127-BHHX", "A routed qube is named by its Qubes source address, which Qubes keeps it from forging", "active", "invariant")

#: SW_261127_G26T is SW-261127-G26T: apply changes only what torii owns: its template, its proxy qubes, its policy files, and named workload settings
SW_261127_G26T = Spec("SW-261127-G26T", "apply changes only what torii owns: its template, its proxy qubes, its policy files, and named workload settings", "active", "invariant")

#: SW_261127_H3W9 is SW-261127-H3W9: The HTTP proxy carries one host per connection
SW_261127_H3W9 = Spec("SW-261127-H3W9", "The HTTP proxy carries one host per connection", "active", "invariant")

#: SW_261127_HSHY is SW-261127-HSHY: The proxy package comes from the dom0 package's bundle, with no repository for torii
SW_261127_HSHY = Spec("SW-261127-HSHY", "The proxy package comes from the dom0 package's bundle, with no repository for torii", "active", "invariant")

#: SW_261127_JWK2 is SW-261127-JWK2: A guard loaded at boot keeps the proxy qube forwarding nothing, whatever the program does
SW_261127_JWK2 = Spec("SW-261127-JWK2", "A guard loaded at boot keeps the proxy qube forwarding nothing, whatever the program does", "active", "invariant")

#: SW_261127_KWT2 is SW-261127-KWT2: Each mesh's file changes only its own qubes and its own policy file
SW_261127_KWT2 = Spec("SW-261127-KWT2", "Each mesh's file changes only its own qubes and its own policy file", "active", "invariant")

#: SW_261127_M36P is SW-261127-M36P: Every qrexec call runs in the direction of the connection it carries, and only the declared ones are allowed
SW_261127_M36P = Spec("SW-261127-M36P", "Every qrexec call runs in the direction of the connection it carries, and only the declared ones are allowed", "active", "invariant")

#: SW_261127_M7G9 is SW-261127-M7G9: A name resolves, for the qube and for the proxy, only to addresses the qube may reach under it
SW_261127_M7G9 = Spec("SW-261127-M7G9", "A name resolves, for the qube and for the proxy, only to addresses the qube may reach under it", "active", "invariant")

#: SW_261127_N3N8 is SW-261127-N3N8: In a qube with no netvm, the client listens on loopback only, one qrexec call per connection
SW_261127_N3N8 = Spec("SW-261127-N3N8", "In a qube with no netvm, the client listens on loopback only, one qrexec call per connection", "active", "invariant")

#: SW_261127_PXX9 is SW-261127-PXX9: A connection is decided against the entry of the qube it came from, by the way it arrived
SW_261127_PXX9 = Spec("SW-261127-PXX9", "A connection is decided against the entry of the qube it came from, by the way it arrived", "active", "invariant")

#: SW_261127_QMVP is SW-261127-QMVP: apply goes on past a proxy's config only once the proxy reports running it
SW_261127_QMVP = Spec("SW-261127-QMVP", "apply goes on past a proxy's config only once the proxy reports running it", "active", "invariant")

#: SW_261127_QQYS is SW-261127-QQYS: UDP is decided per flow, and each qube has a cap on open flows
SW_261127_QQYS = Spec("SW-261127-QQYS", "UDP is decided per flow, and each qube has a cap on open flows", "active", "bound")

#: SW_261127_T7GG is SW-261127-T7GG: An inbound connection reaches a qube only on a port its entry or a service lists
SW_261127_T7GG = Spec("SW-261127-T7GG", "An inbound connection reaches a qube only on a port its entry or a service lists", "active", "invariant")

#: SW_261127_VPGK is SW-261127-VPGK: A qube adopted records its netvm once; a qube leaving the file is left with no netvm
SW_261127_VPGK = Spec("SW-261127-VPGK", "A qube adopted records its netvm once; a qube leaving the file is left with no netvm", "active", "invariant")

#: SW_261127_WNYB is SW-261127-WNYB: Every address is in exactly one zone, and a forbidden address is never dialled
SW_261127_WNYB = Spec("SW-261127-WNYB", "Every address is in exactly one zone, and a forbidden address is never dialled", "active", "invariant")

#: SW_261127_XGDY is SW-261127-XGDY: A proxy takes its config whole or not at all, and a failed check never replaces the running one
SW_261127_XGDY = Spec("SW-261127-XGDY", "A proxy takes its config whole or not at all, and a failed check never replaces the running one", "active", "invariant")

#: QubesAntispoof is an observation of qubes: A Qubes netvm drops packets arriving on a qube's `vif` interface whose source address is not that qube's own; a qube cannot send as another.
QubesAntispoof = Observation("QubesAntispoof", "qubes", "unsure")

#: QubesConnectTCPDenied is an observation of qubes: Qubes' own policy denies `qubes.ConnectTCP` between qubes unless a policy file allows the call.
QubesConnectTCPDenied = Observation("QubesConnectTCPDenied", "qubes", "unsure")
