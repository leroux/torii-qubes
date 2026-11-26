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

#: STK_261126_346B is STK-261126-346B: A qube's traffic leaves the way its entry routes it, or not at all
STK_261126_346B = Spec("STK-261126-346B", "A qube's traffic leaves the way its entry routes it, or not at all", "planned", "")

#: STK_261126_4Q75 is STK-261126-4Q75: The owner reads back what each qube can reach, its drift from the file, and why each connection was decided
STK_261126_4Q75 = Spec("STK-261126-4Q75", "The owner reads back what each qube can reach, its drift from the file, and why each connection was decided", "planned", "")

#: STK_261126_B4CP is STK-261126-B4CP: One file in dom0 declares every qube's connectivity, and apply makes the machine match it
STK_261126_B4CP = Spec("STK-261126-B4CP", "One file in dom0 declares every qube's connectivity, and apply makes the machine match it", "planned", "")

#: STK_261126_KHZB is STK-261126-KHZB: Each qube can be its own tailnet device, without merging compartments or keeping a credential in dom0
STK_261126_KHZB = Spec("STK-261126-KHZB", "Each qube can be its own tailnet device, without merging compartments or keeping a credential in dom0", "planned", "")

#: STK_261126_KZK8 is STK-261126-KZK8: A qube gets exactly the connections its entry lists, in each direction, and nothing else
STK_261126_KZK8 = Spec("STK-261126-KZK8", "A qube gets exactly the connections its entry lists, in each direction, and nothing else", "planned", "")

#: STK_261126_SM33 is STK-261126-SM33: torii leaves Qubes' isolation no weaker than it found it
STK_261126_SM33 = Spec("STK-261126-SM33", "torii leaves Qubes' isolation no weaker than it found it", "planned", "")
