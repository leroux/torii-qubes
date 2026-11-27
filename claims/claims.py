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

#: STK_261126_346B is STK-261126-346B: torii MUST send an egress qube's internet traffic only through that egress's exit nodes, and MUST refuse it when none is healthy
STK_261126_346B = Spec("STK-261126-346B", "torii MUST send an egress qube's internet traffic only through that egress's exit nodes, and MUST refuse it when none is healthy", "planned", "")

#: STK_261126_4Q75 is STK-261126-4Q75: torii show MUST report each qube's reach from the live machine, and torii check MUST name every difference from the file
STK_261126_4Q75 = Spec("STK-261126-4Q75", "torii show MUST report each qube's reach from the live machine, and torii check MUST name every difference from the file", "planned", "")

#: STK_261126_B4CP is STK-261126-B4CP: torii apply MUST change only what torii owns, and MUST make exactly the steps torii plan showed
STK_261126_B4CP = Spec("STK-261126-B4CP", "torii apply MUST change only what torii owns, and MUST make exactly the steps torii plan showed", "planned", "")

#: STK_261126_KHZB is STK-261126-KHZB: torii MUST give each tailnet identity to one qube, join it only with a person's approval, and keep no tailnet credential in dom0
STK_261126_KHZB = Spec("STK-261126-KHZB", "torii MUST give each tailnet identity to one qube, join it only with a person's approval, and keep no tailnet credential in dom0", "planned", "")

#: STK_261126_KZK8 is STK-261126-KZK8: torii MUST relay a carried qube's connection only if the qube's entry allows it, and MUST refuse every other connection
STK_261126_KZK8 = Spec("STK-261126-KZK8", "torii MUST relay a carried qube's connection only if the qube's entry allows it, and MUST refuse every other connection", "planned", "")

#: STK_261126_SM33 is STK-261126-SM33: torii MUST NOT add a qrexec call, network path or dom0 exposure beyond those the file declares
STK_261126_SM33 = Spec("STK-261126-SM33", "torii MUST NOT add a qrexec call, network path or dom0 exposure beyond those the file declares", "planned", "")

#: SW_261127_2JH3 is SW-261127-2JH3: plan refuses before any step a file that names what this machine cannot be given
SW_261127_2JH3 = Spec("SW-261127-2JH3", "plan refuses before any step a file that names what this machine cannot be given", "active", "invariant")

#: SW_261127_2R9T is SW-261127-2R9T: The proxy MUST count a name entry only for a name the qube asked for, or resolved through the proxy to the address dialled
SW_261127_2R9T = Spec("SW-261127-2R9T", "The proxy MUST count a name entry only for a name the qube asked for, or resolved through the proxy to the address dialled", "active", "invariant")

#: SW_261127_2XS6 is SW-261127-2XS6: The proxy MUST match an allow entry by exact name or subdomain, port and protocol, treating an entry without a protocol as TCP only
SW_261127_2XS6 = Spec("SW-261127-2XS6", "The proxy MUST match an allow entry by exact name or subdomain, port and protocol, treating an entry without a protocol as TCP only", "active", "invariant")

#: SW_261127_3Q23 is SW-261127-3Q23: Every proxy qube has IPv6 off, for itself and every qube behind it
SW_261127_3Q23 = Spec("SW-261127-3Q23", "Every proxy qube has IPv6 off, for itself and every qube behind it", "active", "invariant")

#: SW_261127_3TPX is SW-261127-3TPX: An identity MUST sign in by a link a person approves, or by a one-time key that only the proxy qube keeps
SW_261127_3TPX = Spec("SW-261127-3TPX", "An identity MUST sign in by a link a person approves, or by a one-time key that only the proxy qube keeps", "active", "invariant")

#: SW_261127_62WV is SW-261127-62WV: show reports each qube's reach from the live machine, never from the file
SW_261127_62WV = Spec("SW-261127-62WV", "show reports each qube's reach from the live machine, never from the file", "active", "invariant")

#: SW_261127_63CS is SW-261127-63CS: apply makes the plan's steps in order, stops at the first that fails, and a second run continues
SW_261127_63CS = Spec("SW-261127-63CS", "apply makes the plan's steps in order, stops at the first that fails, and a second run continues", "active", "invariant")

#: SW_261127_6S2F is SW-261127-6S2F: The proxy MUST check an allowlisted connection's TLS server name before relaying any of the client's bytes
SW_261127_6S2F = Spec("SW-261127-6S2F", "The proxy MUST check an allowlisted connection's TLS server name before relaying any of the client's bytes", "active", "invariant")

#: SW_261127_6YGC is SW-261127-6YGC: The egress selector MUST NOT clear an exit node, and MUST switch in one change from the old node to the new
SW_261127_6YGC = Spec("SW-261127-6YGC", "The egress selector MUST NOT clear an exit node, and MUST switch in one change from the old node to the new", "active", "invariant")

#: SW_261127_7C3Y is SW-261127-7C3Y: The proxy MUST take a qrexec caller's identity from what dom0 sets, never from what the caller sends
SW_261127_7C3Y = Spec("SW-261127-7C3Y", "The proxy MUST take a qrexec caller's identity from what dom0 sets, never from what the caller sends", "active", "invariant")

#: SW_261127_8DFY is SW-261127-8DFY: The egress selector MUST judge an exit node healthy only while it is listed, answers pings, and is not failing every real connection
SW_261127_8DFY = Spec("SW-261127-8DFY", "The egress selector MUST judge an exit node healthy only while it is listed, answers pings, and is not failing every real connection", "active", "bound")

#: SW_261127_8PTP is SW-261127-8PTP: The proxy MUST NOT send Tailscale client logs unless the file asks, and MUST NOT map ports
SW_261127_8PTP = Spec("SW-261127-8PTP", "The proxy MUST NOT send Tailscale client logs unless the file asks, and MUST NOT map ports", "active", "invariant")

#: SW_261127_9BNW is SW-261127-9BNW: The machine file is read whole and refused whole when a key, a network or a value is wrong
SW_261127_9BNW = Spec("SW-261127-9BNW", "The machine file is read whole and refused whole when a key, a network or a value is wrong", "active", "invariant")

#: SW_261127_B6Z6 is SW-261127-B6Z6: The proxy qube MUST hand every routed qube's traffic to the program and MUST NOT forward any of it
SW_261127_B6Z6 = Spec("SW-261127-B6Z6", "The proxy qube MUST hand every routed qube's traffic to the program and MUST NOT forward any of it", "active", "invariant")

#: SW_261127_B8BM is SW-261127-B8BM: The status service is read-only: it reports, and changes nothing in the proxy
SW_261127_B8BM = Spec("SW-261127-B8BM", "The status service is read-only: it reports, and changes nothing in the proxy", "active", "invariant")

#: SW_261127_BHHX is SW-261127-BHHX: The proxy MUST identify a routed qube by its Qubes source address, which Qubes keeps it from forging
SW_261127_BHHX = Spec("SW-261127-BHHX", "The proxy MUST identify a routed qube by its Qubes source address, which Qubes keeps it from forging", "active", "invariant")

#: SW_261127_BNFC is SW-261127-BNFC: The egress selector MUST use exit nodes in the file's order, failing over at once and back only after a hold-down
SW_261127_BNFC = Spec("SW-261127-BNFC", "The egress selector MUST use exit nodes in the file's order, failing over at once and back only after a hold-down", "active", "invariant")

#: SW_261127_DH6R is SW-261127-DH6R: torii MUST only propose grants for the tailnet, and MUST NOT change the tailnet's policy
SW_261127_DH6R = Spec("SW-261127-DH6R", "torii MUST only propose grants for the tailnet, and MUST NOT change the tailnet's policy", "active", "invariant")

#: SW_261127_G26T is SW-261127-G26T: apply changes only what torii owns: its template, its proxy qubes, its policy files, and named workload settings
SW_261127_G26T = Spec("SW-261127-G26T", "apply changes only what torii owns: its template, its proxy qubes, its policy files, and named workload settings", "active", "invariant")

#: SW_261127_H3W9 is SW-261127-H3W9: The proxy's HTTP proxy MUST carry only one host and port per connection
SW_261127_H3W9 = Spec("SW-261127-H3W9", "The proxy's HTTP proxy MUST carry only one host and port per connection", "active", "invariant")

#: SW_261127_HJNX is SW-261127-HJNX: The proxy MUST refuse an egress's connections at once, and fail its DNS, while the egress has no healthy exit node
SW_261127_HJNX = Spec("SW-261127-HJNX", "The proxy MUST refuse an egress's connections at once, and fail its DNS, while the egress has no healthy exit node", "active", "invariant")

#: SW_261127_HSHY is SW-261127-HSHY: The proxy package comes from the dom0 package's bundle, with no repository for torii
SW_261127_HSHY = Spec("SW-261127-HSHY", "The proxy package comes from the dom0 package's bundle, with no repository for torii", "active", "invariant")

#: SW_261127_JWK2 is SW-261127-JWK2: The proxy template MUST load a guard at boot that keeps the proxy qube forwarding nothing, whatever the program does
SW_261127_JWK2 = Spec("SW-261127-JWK2", "The proxy template MUST load a guard at boot that keeps the proxy qube forwarding nothing, whatever the program does", "active", "invariant")

#: SW_261127_K5TK is SW-261127-K5TK: The proxy MUST dial an egress's traffic only into the egress identity's own network stack, never the system network
SW_261127_K5TK = Spec("SW-261127-K5TK", "The proxy MUST dial an egress's traffic only into the egress identity's own network stack, never the system network", "active", "invariant")

#: SW_261127_KWT2 is SW-261127-KWT2: Each mesh's file changes only its own qubes and its own policy file
SW_261127_KWT2 = Spec("SW-261127-KWT2", "Each mesh's file changes only its own qubes and its own policy file", "active", "invariant")

#: SW_261127_KYZW is SW-261127-KYZW: The proxy MUST send a qube's LAN traffic directly, never through its egress, and torii plan MUST say so
SW_261127_KYZW = Spec("SW-261127-KYZW", "The proxy MUST send a qube's LAN traffic directly, never through its egress, and torii plan MUST say so", "active", "invariant")

#: SW_261127_M36P is SW-261127-M36P: torii's qrexec policy MUST allow only calls that run in the direction of the connection they carry, and only those the file declares
SW_261127_M36P = Spec("SW-261127-M36P", "torii's qrexec policy MUST allow only calls that run in the direction of the connection they carry, and only those the file declares", "active", "invariant")

#: SW_261127_M7G9 is SW-261127-M7G9: The proxy MUST resolve a name, for the qube and for itself, only to addresses the qube may reach under that name
SW_261127_M7G9 = Spec("SW-261127-M7G9", "The proxy MUST resolve a name, for the qube and for itself, only to addresses the qube may reach under that name", "active", "invariant")

#: SW_261127_N3N8 is SW-261127-N3N8: torii-client MUST listen on loopback only and MUST make one qrexec call per connection
SW_261127_N3N8 = Spec("SW-261127-N3N8", "torii-client MUST listen on loopback only and MUST make one qrexec call per connection", "active", "invariant")

#: SW_261127_P8H7 is SW-261127-P8H7: torii MUST give a tagged identity exactly the file's tags, prefixed by the machine, and torii check MUST name any other
SW_261127_P8H7 = Spec("SW-261127-P8H7", "torii MUST give a tagged identity exactly the file's tags, prefixed by the machine, and torii check MUST name any other", "active", "invariant")

#: SW_261127_PXX9 is SW-261127-PXX9: The proxy MUST decide a connection against the entry of the qube it came from, and only by a way that entry offers
SW_261127_PXX9 = Spec("SW-261127-PXX9", "The proxy MUST decide a connection against the entry of the qube it came from, and only by a way that entry offers", "active", "invariant")

#: SW_261127_QMVP is SW-261127-QMVP: apply goes on past a proxy's config only once the proxy reports running it
SW_261127_QMVP = Spec("SW-261127-QMVP", "apply goes on past a proxy's config only once the proxy reports running it", "active", "invariant")

#: SW_261127_QQYS is SW-261127-QQYS: The proxy MUST decide UDP once per flow and MUST cap each qube's open flows
SW_261127_QQYS = Spec("SW-261127-QQYS", "The proxy MUST decide UDP once per flow and MUST cap each qube's open flows", "active", "bound")

#: SW_261127_R4CD is SW-261127-R4CD: Every connection the proxy decides is logged with its verdict and the rule that made it
SW_261127_R4CD = Spec("SW-261127-R4CD", "Every connection the proxy decides is logged with its verdict and the rule that made it", "active", "invariant")

#: SW_261127_SMYC is SW-261127-SMYC: check names each difference it looks for between the live machine and the file, and fails on one
SW_261127_SMYC = Spec("SW-261127-SMYC", "check names each difference it looks for between the live machine and the file, and fails on one", "active", "invariant")

#: SW_261127_T7GG is SW-261127-T7GG: The proxy MUST relay an inbound connection to a qube only on a port its entry or a service lists
SW_261127_T7GG = Spec("SW-261127-T7GG", "The proxy MUST relay an inbound connection to a qube only on a port its entry or a service lists", "active", "invariant")

#: SW_261127_VPGK is SW-261127-VPGK: A qube adopted records its netvm once; a qube leaving the file is left with no netvm
SW_261127_VPGK = Spec("SW-261127-VPGK", "A qube adopted records its netvm once; a qube leaving the file is left with no netvm", "active", "invariant")

#: SW_261127_WNGN is SW-261127-WNGN: The proxy MUST run each identity as a tailnet device of its own, with its own name and state
SW_261127_WNGN = Spec("SW-261127-WNGN", "The proxy MUST run each identity as a tailnet device of its own, with its own name and state", "active", "invariant")

#: SW_261127_WNYB is SW-261127-WNYB: The proxy MUST place every address in exactly one zone and MUST NOT dial a forbidden address
SW_261127_WNYB = Spec("SW-261127-WNYB", "The proxy MUST place every address in exactly one zone and MUST NOT dial a forbidden address", "active", "invariant")

#: SW_261127_WSWG is SW-261127-WSWG: An egress's identity MUST carry only internet traffic, and torii grants MUST propose it nothing else
SW_261127_WSWG = Spec("SW-261127-WSWG", "An egress's identity MUST carry only internet traffic, and torii grants MUST propose it nothing else", "active", "invariant")

#: SW_261127_XGDY is SW-261127-XGDY: A proxy takes its config whole or not at all, and a failed check never replaces the running one
SW_261127_XGDY = Spec("SW-261127-XGDY", "A proxy takes its config whole or not at all, and a failed check never replaces the running one", "active", "invariant")

#: SW_261127_Z5ST is SW-261127-Z5ST: explain gives the decision the proxy would make, without sending anything
SW_261127_Z5ST = Spec("SW-261127-Z5ST", "explain gives the decision the proxy would make, without sending anything", "active", "invariant")

#: QubesAntispoof is an observation of qubes: A Qubes netvm drops packets arriving on a qube's `vif` interface whose source address is not that qube's own; a qube cannot send as another.
QubesAntispoof = Observation("QubesAntispoof", "qubes", "unsure")

#: QubesConnectTCPDenied is an observation of qubes: Qubes' own policy denies `qubes.ConnectTCP` between qubes unless a policy file allows the call.
QubesConnectTCPDenied = Observation("QubesConnectTCPDenied", "qubes", "unsure")
