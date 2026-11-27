"""Golden and property tests for the dom0 tool. Run: python3 -m pytest tests
(or python3 -m unittest discover tests). UPDATE_GOLDEN=1 rewrites goldens."""

import copy
import json
import os
import textwrap
import unittest

import claims
from torii import derive, machine, plan, show


def mparse(text: str):
    """machine.parse with the machine's name added when a test leaves it out,
    and `defaults: {network: none}` when it has no defaults (network is
    required)."""
    if "defaults:" not in text:
        text = "defaults: {network: none}\n" + text
    return machine.parse(text if "machine:" in text else "machine: m1\n" + text)


from torii.live import Snapshot

TODAY = "2026-11-10"
plan.today = lambda: TODAY  # adoption records carry a date; keep goldens stable

HERE = os.path.dirname(__file__)
G = os.path.join(HERE, "golden")
VERSION = "0.1.0-test"


def golden(name: str, got: str) -> None:
    path = os.path.join(G, name)
    if os.environ.get("UPDATE_GOLDEN") or not os.path.exists(path):
        with open(path, "w", encoding="utf-8") as f:
            f.write(got)
    with open(path, encoding="utf-8") as f:
        want = f.read()
    assert got == want, f"{name} differs; UPDATE_GOLDEN=1 to accept:\n{got}"


def snap(name="snapshot-fresh.json") -> Snapshot:
    with open(os.path.join(G, name), encoding="utf-8") as f:
        return Snapshot.from_json(f.read())


def full_machine() -> machine.Machine:
    return machine.load(os.path.join(G, "full.yaml"))


def simulate(s: Snapshot, steps) -> Snapshot:
    """What apply would leave behind, as a snapshot: replays each step."""
    s = copy.deepcopy(s)
    from torii.live import QubeInfo
    for st in steps:
        q = s.qubes.get(st.target)
        if st.kind == "template-install":
            s.qubes[st.target] = QubeInfo(st.target, "TemplateVM", "Halted", None, "", False, 90, "", False,
                                          "black", [], {})
        elif st.kind in ("clone", "create"):
            klass = "TemplateVM" if st.kind == "clone" else "AppVM"
            s.qubes[st.target] = QubeInfo(st.target, klass, "Halted", st.data.get("template"), "", False,
                                          91 + len(s.qubes), "", False, st.data.get("label", "black"),
                                          [derive.OWNED_TAG] + ([st.data["mesh_tag"]] if st.data.get("mesh_tag") else []),
                                          {})
        elif st.kind == "template-package":
            q.features["torii-version"] = st.data["version"]
        elif st.kind == "pref":
            setattr(q, st.data["key"], st.data["value"])
        elif st.kind == "feature-set":
            q.features[st.data["key"]] = st.data["value"]
        elif st.kind == "feature-del":
            q.features.pop(st.data["key"], None)
        elif st.kind == "tag-add":
            q.tags = sorted(set(q.tags) | {st.data["tag"]})
        elif st.kind == "tag-del":
            q.tags = sorted(set(q.tags) - {st.data["tag"]})
        elif st.kind == "proxy-config":
            for k, v in st.data["features"].items():
                if v is None:
                    q.features.pop(k, None)
                else:
                    q.features[k] = v
        elif st.kind in ("restart", "proxy-ready"):
            q.state = "Running"
            if st.data.get("template"):
                q.template = st.data["template"]
            q.features[derive.STARTED_VERSION_FEATURE] = st.data["package"]
        elif st.kind == "policy" and st.target == derive.POLICY_NAME:
            s.policy = st.data["text"]
        elif st.kind == "policy":
            s.other_policy[st.target] = st.data["text"]
        elif st.kind == "remove":
            del s.qubes[st.target]
        else:
            raise AssertionError(st.kind)
    return s


SERVICES = """
qubes: {}
services:
  notes: { qube: personal, tags: [tag:notes], ports: [{port: 443, target: 3000, tls: yes}, {port: 8443, target: 3000, tls: yes}] }
  chat:   { qube: personal, tags: [tag:chat],   ports: [{port: 443, target: 8008, tls: yes}] }
"""


def as_mesh(s: Snapshot, m) -> Snapshot:
    """The snapshot as the tool reads it for mesh m: its own policy file in
    policy, every other file (the default mesh's included) in other_policy."""
    s = copy.deepcopy(s)
    files = dict(s.other_policy)
    if s.policy is not None:
        files[derive.POLICY_NAME] = s.policy
    s.policy = files.pop(derive.policy_name(m), None)
    s.other_policy = files
    return s


def mpatched(m, qube):
    """m with one more qube (no network card, nothing else)."""
    return mparse(f"mesh: {m.mesh}\nmachine: {m.name}\nproxy: {{name: {m.default_proxy}}}\nqubes: {{{qube}: {{}}}}")


class MachineFile(unittest.TestCase):
    def bad(self, text, msg):
        with self.assertRaises(machine.FileError) as e:
            mparse(text)
        self.assertIn(msg, str(e.exception))

    def test_defaults_resolve(self):
        m = full_machine()
        p = m.qubes["torii-probe"]
        self.assertEqual((p.network_card, p.internet, p.identity, p.tags), (False, "none", "tagged", ("tag:m1-torii-test", "tag:m1")))
        self.assertEqual(p.inbound[1], machine.Inbound(8443, 8080))
        self.assertEqual((p.socks_port, p.http_port), (0, 0), "no outbound, no proxy ports")
        j = m.qubes["torii-probe2"]
        self.assertEqual((j.socks_port, j.http_port, j.tags), (1080, 3128, ("tag:m1-torii-job", "tag:m1")))
        r = m.qubes["personal"]
        self.assertEqual((r.link, r.identity, r.tags, r.socks_port), ("routed", "login", (), 0))

    def test_example_file_parses(self):
        machine.load(os.path.join(HERE, "..", "..", "examples", "machine.yaml"))
        machine.load(os.path.join(HERE, "..", "..", "proxy", "internal", "engine", "testdata", "sim", "machine.yaml"))
        machine.load(os.path.join(HERE, "..", "..", "dev", "headscale", "torii-test.yaml"))

    def test_every_machine_file_in_the_docs_parses(self):
        """Every ```yaml block in the README and docs/ parses with the tool's
        own parser. An indented block is a fragment of qubes: and is parsed
        under it, unless it starts with machine: (a whole file in a list); so is
        a block whose first key is not a top-level key (qube entries, as the
        README shows them). Blocks that are not machine files (none yet) would be
        marked with a first line `# not a machine file`."""
        import glob
        import re
        root = os.path.join(HERE, "..", "..")
        files = [os.path.join(root, "README.md")] + sorted(glob.glob(os.path.join(root, "docs", "*.md")))
        n = 0
        for path in files:
            with open(path, encoding="utf-8") as f:
                text = f.read()
            for i, block in enumerate(re.findall(r"```yaml\n(.*?)```", text, re.S)):
                if block.startswith("# not a machine file"):
                    continue
                first = next((l for l in block.splitlines() if l.strip() and not l.lstrip().startswith("#")), "")
                if first.lstrip().startswith("machine:"):
                    doc = textwrap.dedent(block)  # a whole file, indented inside a list item
                else:
                    key = first.strip().split(":", 1)[0]
                    if first.startswith(" "):
                        doc = "qubes:\n" + block
                    elif key not in machine.TOP_KEYS:  # qube entries, as the README shows them
                        doc = "qubes:\n" + textwrap.indent(block, "  ")
                    else:
                        doc = block
                with self.subTest(file=os.path.basename(path), block=i):
                    try:
                        mparse("defaults: {network: none, tailnet: {tags: [t]}}\n" + doc if "defaults:" not in doc else doc)
                    except machine.FileError as e:
                        self.fail(f"{os.path.basename(path)} yaml block {i}: {e}\n{block}")
                n += 1
        self.assertGreater(n, 3, "found the docs' yaml blocks")

    def test_errors(self):
        claims.verifies(self, claims.SW_261127_9BNW, "unknown-key-refused")
        self.bad("qubes: {a: {netwrk-card: yes}}", "unknown key 'netwrk-card'")
        self.bad("qubes: {a: {outbound: {internet: ful}}}", "none, all, or a list")
        self.bad("qubes: {a: {outbound: {internet: pypi.org}}}", "none, all, or a list")
        self.bad("qubes: {a: {outbound: {internet: [some thing]}}}", "not a host")
        self.bad("qubes: {a: {outbound: {internet: []}}}", "none, all, or a list")
        self.bad("qubes: {a: {outbound: {internet: ['pypi.org:99999']}}}", "bad port")
        self.bad("qubes: {a: {outbound: {lan: ['10.0.0.0/33']}}}", "not a host")
        self.bad("qubes: {a: {outbound: {wan: all}}}", "unknown key 'wan'")
        self.bad("qubes: {a: {tailnet: {identity: tagged}}}", "needs tags")
        self.bad("qubes: {a: {tailnet: {inbound: [80]}}}", "need an identity")
        self.bad("qubes: {a: {outbound: {tailnet: yes}}}", "need an identity")
        self.bad("qubes: {a: {network: proxy, forwards: {x: 'h:1'}}}", "forwards are for qubes without")
        self.bad("qubes: {a: {outbound: {via: ex, internet: all}}}", "outbound.via: removed")
        self.bad("qubes: {sys-torii: {}}", "is a proxy qube")
        self.bad("qubes: {a: {proxy: nope}}", "no proxy 'nope'")
        self.bad("defaults: {tailnet: {inbound: [1]}}", "unknown key 'inbound'")

    def test_protocols(self):
        m = mparse("qubes: {r: {network: proxy, outbound: {internet: [pypi.org, 'example.com:443/udp', '8.8.8.8:53/udp', '10.0.0.0/8/tcp']}}}")
        self.assertEqual(m.qubes["r"].internet_allow, ("pypi.org", "example.com:443/udp", "8.8.8.8:53/udp", "10.0.0.0/8/tcp"))
        self.bad("qubes: {r: {network: proxy, outbound: {internet: ['pypi.org:443/sctp']}}}", "bad port")

    def test_tailnet_list(self):
        m = mparse("""
qubes:
  j:
    outbound: {tailnet: [notes, "chat:443", "100.64.0.0/10:22"]}
    tailnet: {identity: tagged, tags: [job]}
  k: {outbound: {tailnet: yes}, tailnet: {identity: tagged, tags: [job]}}
""")
        j, k = m.qubes["j"], m.qubes["k"]
        self.assertEqual((j.tailnet_outbound, j.tailnet_allow), (True, ("notes", "chat:443", "100.64.0.0/10:22")))
        self.assertEqual((k.tailnet_outbound, k.tailnet_allow), (True, ()))
        cfg = derive.proxy_config(m, "sys-torii", {}, "v")["workloads"]
        self.assertEqual(cfg["j"]["tailnet_allow"], ["notes", "chat:443", "100.64.0.0/10:22"])
        self.assertNotIn("tailnet_allow", cfg["k"])
        self.bad("qubes: {a: {outbound: {tailnet: []}, tailnet: {identity: tagged, tags: [x]}}}", "a list of devices")
        self.bad("qubes: {a: {outbound: {tailnet: ['notes:0']}, tailnet: {identity: tagged, tags: [x]}}}", "bad port")
        self.bad("qubes: {a: {outbound: {tailnet: [notes]}}}", "need an identity")

    def test_machine_and_tags(self):
        with self.assertRaises(machine.FileError) as e:
            machine.parse("qubes: {}")
        self.assertIn("machine: required", str(e.exception))
        m = mparse("""
defaults: {network: none, tailnet: {tags: [server]}}
qubes:
  job-1: {tailnet: {identity: tagged, tags: [job]}}
  w: {tailnet: {identity: tagged, hostname: wiki}}
  me: {network: proxy, tailnet: {identity: login}}
services:
  chat: {qube: g, tags: [tag:chat], ports: [443]}
  backup: {qube: v, tags: [backup], ports: [22]}
""")
        q, x = m.qubes, m.services
        self.assertEqual((q["job-1"].hostname, q["job-1"].tags), ("m1-job-1", ("tag:m1-job", "tag:m1")))
        self.assertEqual((q["w"].hostname, q["w"].tags), ("wiki", ("tag:m1-server", "tag:m1")))
        self.assertEqual(q["me"].tags, (), "a person's device carries no tags")
        self.assertEqual((x["chat"].hostname, x["chat"].tags), ("chat", ("tag:chat", "tag:m1")))
        self.assertEqual(x["backup"].tags, ("tag:m1-backup", "tag:m1"))
        self.assertIn("tag:m1", derive.grants(m)["tagOwners"])
        self.bad("qubes: {a: {tailnet: {identity: tagged, tags: [tag:server]}}}", "full tag")
        self.bad("qubes: {a: {tailnet: {identity: tagged, tags: [Server]}}}", "is not a tag")
        with self.assertRaises(machine.FileError):
            machine.parse("machine: M_1\nqubes: {}")

    def test_earlier_schema_says_where_keys_went(self):
        claims.verifies(self, claims.SW_261127_9BNW, "earlier-key-redirected")
        self.bad("qubes: {a: {internet: full}}", "moved to outbound.internet")
        self.bad("qubes: {a: {internet-allow: [x.org]}}", "moved to the list in outbound.internet")
        self.bad("qubes: {a: {internet-via: ex}}", "moved to egress:")
        self.bad("qubes: {a: {tailnet: {outbound: yes}}}", "moved to outbound.tailnet")
        self.bad("defaults: {internet: none}", "moved to outbound.internet")

    def test_zones(self):
        m = mparse("""
defaults: {network: none, tailnet: {tags: [t]}, outbound: {lan: none}}
qubes:
  a: {outbound: {internet: all}}
  b: {outbound: {internet: [pypi.org, "*.hf.co:443", "93.184.215.0/24:80"], lan: ["192.168.1.20:5432", "[fd00::1]:80"]}}
  c: {}
""")
        a, b, c = m.qubes["a"], m.qubes["b"], m.qubes["c"]
        self.assertEqual((a.internet, a.lan), ("all", "none"))
        self.assertEqual((b.internet, b.lan), ("allowlist", "allowlist"))
        self.assertEqual(b.lan_allow, ("192.168.1.20:5432", "[fd00::1]:80"))
        self.assertEqual((c.internet, c.lan, c.has_outbound), ("none", "none", False))
        cfg = derive.proxy_config(m, "sys-torii", {}, "v")["workloads"]
        self.assertEqual((cfg["a"]["internet"], "lan" in cfg["a"]), ("full", False))
        self.assertEqual(cfg["b"]["lan_allow"], ["192.168.1.20:5432", "[fd00::1]:80"])
        self.assertNotIn("c", cfg)


class Plans(unittest.TestCase):
    def test_fresh_machine(self):
        steps = plan.make(full_machine(), snap(), VERSION)
        golden("plan-fresh.txt", "\n".join(str(s) for s in steps) + "\n")

    def test_policy(self):
        golden("policy.txt", derive.policy(full_machine()))

    def test_proxy_config(self):
        s = snap()
        addrs = {"personal": s.qubes["personal"].address}
        cfg = derive.proxy_config(full_machine(), "sys-torii", addrs, "v1")
        golden("proxy-config.json", json.dumps(cfg, indent=1, sort_keys=True) + "\n")

    def test_second_apply_changes_nothing(self):
        claims.verifies(self, claims.SW_261127_63CS, "second-apply-empty")
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        self.assertEqual([str(s) for s in plan.make(m, after, VERSION)], [])

    def test_removed_qube_is_reset(self):
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        with open(os.path.join(G, "full.yaml"), encoding="utf-8") as f:
            smaller = mparse(f.read().split("  personal:")[0])
        steps = plan.make(smaller, after, VERSION)
        golden("plan-remove-personal.txt", "\n".join(str(s) for s in steps) + "\n")
        final = simulate(after, steps)
        p = final.qubes["personal"]
        self.assertEqual(p.netvm, "", "safe default: offline")
        self.assertNotIn(derive.MANAGED_TAG, p.tags)
        self.assertEqual(plan.make(smaller, final, VERSION), [])

    def test_new_package_restarts_running_proxies(self):
        claims.verifies(self, claims.SW_261127_HSHY, "running-proxy-restarted")
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        after.qubes["personal"].state = "Running"
        steps = plan.make(m, after, VERSION + ".1")
        self.assertEqual([str(s) for s in steps], [
            "torii-fedora-44-minimal: install the proxy package 0.1.0-test.1 (has 0.1.0-test)",
            "sys-torii: restart it on the new package (detaching and reattaching personal)",
        ])

    def test_proxy_started_on_an_older_package_is_restarted(self):
        """Another mesh's apply upgraded the template they share: this
        mesh's running proxy still runs the old package until it restarts,
        though the template has nothing left to install."""
        m = full_machine()
        old = simulate(snap(), plan.make(m, snap(), VERSION))
        old.qubes["sys-torii"].state = "Running"
        old.qubes[m.template].features["torii-version"] = VERSION + ".1"  # by the other mesh
        steps = plan.make(m, old, VERSION + ".1")
        self.assertEqual([str(s) for s in steps], [
            "sys-torii: restart it on the new package",
        ])
        self.assertEqual(plan.make(m, simulate(old, steps), VERSION + ".1"), [])

    def test_new_base_template_moves_the_proxies(self):
        """A machine whose file named another template (Debian, torii-template)
        moves to the new base with plain apply: a new template, each proxy
        moved onto it with one restart, the old template removed."""
        with open(os.path.join(G, "full.yaml"), encoding="utf-8") as f:
            text = f.read()
        old_m = mparse("template: torii-template\nbase-template: debian-13-minimal\n" + text)
        before = simulate(snap(), plan.make(old_m, snap(), VERSION))
        before.qubes["sys-torii"].state = "Running"
        before.qubes["personal"].state = "Running"
        m = full_machine()
        steps = plan.make(m, before, VERSION)
        self.assertEqual([str(s) for s in steps], [
            "fedora-44-minimal: download the base template (qvm-template install)",
            "torii-fedora-44-minimal: create the proxy template, a clone of fedora-44-minimal",
            "torii-fedora-44-minimal: install the proxy package 0.1.0-test",
            "sys-torii: move it to torii-fedora-44-minimal and restart it (detaching and reattaching personal)",
            "torii-template: remove the tool's old template (nothing uses it now; torii-fedora-44-minimal replaces it)",
        ])
        after = simulate(before, steps)
        self.assertEqual(after.qubes["sys-torii"].template, "torii-fedora-44-minimal")
        self.assertNotIn("torii-template", after.qubes)
        self.assertIn("debian-13-minimal", after.qubes, "the base template is the person's, not the tool's")
        self.assertEqual(plan.make(m, after, VERSION), [])

    def test_old_template_another_mesh_uses_stays(self):
        claims.verifies(self, claims.SW_261127_KWT2, "shared-template-kept")
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        after.qubes["torii-template"] = copy.deepcopy(after.qubes["torii-fedora-44-minimal"])
        after.qubes["torii-template"].name = "torii-template"
        other = copy.deepcopy(after.qubes["sys-torii"])
        other.name, other.template = "sys-torii-other", "torii-template"
        other.tags = [derive.OWNED_TAG, derive.MESH_TAG + "other"]  # another mesh's proxy
        after.qubes["sys-torii-other"] = other
        self.assertEqual(plan.make(m, after, VERSION), [])

    def test_services_on_a_qube_the_file_does_not_manage(self):
        claims.verifies(self, claims.SW_261127_G26T, "service-host-untouched")
        m = mparse(SERVICES)
        steps = plan.make(m, snap(), VERSION)
        golden("plan-services.txt", "\n".join(str(s) for s in steps) + "\n")
        self.assertFalse([s for s in steps if s.target == "personal"], "its network is not the tool's")
        cfg = derive.proxy_config(m, "sys-torii", {}, "v")
        self.assertEqual(sorted(cfg["workloads"]), ["personal.chat", "personal.notes"])
        self.assertEqual(cfg["workloads"]["personal.chat"]["inbound"], [{"port": 443, "target": 8008, "tls": True}])
        self.assertIn("qubes.ConnectTCP  +8008  sys-torii  personal  allow", derive.policy(m))
        self.assertIn("tag:chat", derive.grants(m)["tagOwners"])
        # Dropping the services leaves the qube's netvm as it was.
        after = simulate(snap(), steps)
        gone = plan.make(mparse("qubes: {}"), after, VERSION)
        self.assertFalse([s for s in gone if s.target == "personal"], gone)
        self.assertEqual(simulate(after, gone).qubes["personal"].netvm, "sys-firewall")

    def test_network_values(self):
        m = mparse("""
proxies: [{name: sys-torii-vpn, netvm: sys-net}]
qubes:
  a: {network: none}
  b: {network: proxy}
  c: {network: proxy, proxy: sys-torii-vpn, outbound: {internet: all}}
  d: {network: default}
  e: {network: sys-net}
""")
        st = {n: derive.qube_state(m, q).netvm for n, q in m.qubes.items()}
        self.assertEqual(st, {"a": "", "b": "sys-torii", "c": "sys-torii-vpn", "d": derive.DEFAULT_NETVM, "e": "sys-net"})
        self.assertEqual(m.qubes["c"].proxy, "sys-torii-vpn")

    def test_not_carried_by_the_proxy(self):
        for e in ("{network: default, outbound: {internet: all}}", "{network: sys-net, tailnet: {identity: tagged, tags: [x]}}",
                  "{network: default, forwards: {x: 'h:1'}}"):
            with self.assertRaises(machine.FileError) as c:
                mparse("qubes: {a: %s}" % e)
            self.assertIn("services:", str(c.exception))

    def test_earlier_keys(self):
        for e, msg in (("{network-card: keep}", "moved to network"), ("{identities: {m: {}}}", "moved to services")):
            with self.assertRaises(machine.FileError) as c:
                mparse("qubes: {a: %s}" % e)
            self.assertIn(msg, str(c.exception))

    def test_service_names(self):
        with self.assertRaises(machine.FileError):
            mparse("defaults: {network: none, tailnet: {tags: [t]}}\nqubes: {q: {tailnet: {identity: tagged, hostname: m}}}\n"
                   "services: {m: {qube: x, ports: [1]}}")  # tailnet name m twice
        with self.assertRaises(machine.FileError):
            mparse("services: {m: {qube: x, tags: [t]}}")  # no ports

    def test_two_meshes_keep_to_their_own(self):
        claims.verifies(self, claims.SW_261127_KWT2, "meshes-apart")
        main = mparse("qubes: {torii-probe: {}, personal: {network: proxy, outbound: {internet: all}}}")
        test = mparse("""
mesh: test
machine: m1-test
proxy: {name: sys-torii-test}
qubes: {torii-probe2: {tailnet: {identity: tagged, tags: [t]}}}
""")
        snap1 = simulate(snap(), plan.make(main, snap(), VERSION))
        steps = plan.make(test, as_mesh(snap1, test), VERSION)
        golden("plan-second-mesh.txt", "\n".join(str(s) for s in steps) + "\n")
        targets = {s.target for s in steps}
        self.assertFalse(targets & {"torii-probe", "personal", "sys-torii", "50-torii"}, "the test mesh touched the main mesh")
        self.assertIn("50-torii-test", targets)
        self.assertEqual(derive.policy_name(test), "50-torii-test")
        snap2 = simulate(as_mesh(snap1, test), steps)
        created = snap2.qubes["sys-torii-test"]
        self.assertEqual(set(created.tags) >= {derive.OWNED_TAG, "torii-mesh-test"}, True)
        # Each mesh's next plan is empty: neither undoes the other.
        self.assertEqual(plan.make(main, as_mesh(snap2, main), VERSION), [])
        self.assertEqual(plan.make(test, as_mesh(snap2, test), VERSION), [])
        # A qube managed by one mesh cannot be claimed by the other.
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mpatched(test, "torii-probe"), as_mesh(snap2, test), VERSION)
        self.assertIn("managed by another mesh", str(e.exception))
        # A mesh's own proxy name cannot be taken by another mesh.
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("mesh: other\nproxy: {name: sys-torii-test}\nqubes: {}"), snap2, VERSION)
        self.assertIn("another mesh", str(e.exception))

    def test_workload_must_exist(self):
        claims.verifies(self, claims.SW_261127_2JH3, "missing-qube-refused")
        m = mparse("defaults: {network: none, tailnet: {tags: [t]}}\nqubes: {nosuch: {}}")
        with self.assertRaises(plan.PlanError) as e:
            plan.make(m, snap(), VERSION)
        self.assertIn("never creates workloads", str(e.exception))
        # Every reason is named at once, and nothing is planned.
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("defaults: {network: none, tailnet: {tags: [t]}}\nqubes: {nosuch: {}, sys-net: {}}"), snap(), VERSION)
        self.assertIn("nosuch is in the file but not on this machine", str(e.exception))
        self.assertIn("sys-net provides network to other qubes", str(e.exception))

    def test_netvms_are_not_workloads(self):
        claims.verifies(self, claims.SW_261127_2JH3, "netvm-not-a-workload")
        with self.assertRaises(plan.PlanError):
            plan.make(mparse("qubes: {sys-net: {}}"), snap(), VERSION)

    def test_foreign_template_name(self):
        with self.assertRaises(plan.PlanError):
            plan.make(mparse("template: fedora-43-xfce\nqubes: {personal: {network: proxy}}"), snap(), VERSION)


class Schema(unittest.TestCase):
    """The file says each fact once: a qube's proxy in proxy:, the kind of
    identity in tailnet.identity, its network explicitly."""

    def test_tailscale_logs_off_unless_asked(self):
        body = "qubes:\n  w: {network: proxy, outbound: {internet: all}}\n"
        off = derive.proxy_config(mparse(body), "sys-torii", {"w": "10.137.0.9"}, "v1")
        self.assertNotIn("tailscale_logs", off)
        on = derive.proxy_config(mparse("tailnet: {tailscale-logs: yes}\n" + body), "sys-torii", {"w": "10.137.0.9"}, "v1")
        self.assertIs(on["tailscale_logs"], True)
        with self.assertRaises(machine.FileError):
            mparse("tailnet: {tailscale-logs: maybe}\n" + body)

    def bad(self, text, msg):
        with self.assertRaises(machine.FileError) as e:
            mparse(text)
        self.assertIn(msg, str(e.exception))

    def test_network_is_required(self):
        claims.verifies(self, claims.SW_261127_9BNW, "network-required")
        with self.assertRaises(machine.FileError) as e:
            machine.parse("machine: m1\nqubes: {a: {outbound: {internet: all}}}")
        self.assertIn("qubes.a.network: required", str(e.exception))
        m = machine.parse("machine: m1\nqubes: {a: {network: proxy}}")
        self.assertEqual(m.qubes["a"].network, "proxy")
        machine.parse("machine: m1\nqubes: {}")  # no qubes, nothing required

    def test_removed_keys_say_what_to_write(self):
        self.bad("manage: all\nqubes: {}", "manage: removed")
        self.bad("manage: listed\nqubes: {}", "manage: removed")
        self.bad("qubes: {a: {tailnet: {identity: proxy, tags: [x]}}}", "write identity: tagged")
        self.bad("proxies: [{name: p2}]\nqubes: {a: {tailnet: {identity: 'proxy:p2', tags: [x]}}}", "write proxy: p2")
        self.bad("proxies: [{name: p2}]\nqubes: {a: {network: 'proxy:p2'}}", "write proxy: p2")

    def test_proxy_key(self):
        m = mparse("""
proxies: [{name: p2}]
qubes:
  a: {network: none, proxy: p2, outbound: {internet: all}}
  b: {network: proxy, proxy: p2, tailnet: {identity: tagged, tags: [x]}}
  c: {network: proxy}
""")
        self.assertEqual({n: q.proxy for n, q in m.qubes.items()}, {"a": "p2", "b": "p2", "c": "sys-torii"})
        self.assertEqual(derive.qube_state(m, m.qubes["b"]).netvm, "p2")
        self.bad("qubes: {a: {proxy: sys-torii-x}}", "no proxy 'sys-torii-x'")

    def test_adopt_and_disposables_keys(self):
        m = mparse("qubes: {a: {adopt: force}, b: {disposables: yes}, c: {}}")
        self.assertEqual([(q.adopt_force, q.disposables) for q in m.qubes.values()],
                         [(True, False), (False, True), (False, False)])
        self.bad("qubes: {a: {adopt: yes}}", "adopt: force")
        self.bad("defaults: {network: none, adopt: force}\nqubes: {}", "unknown key 'adopt'")


def with_qube(s: Snapshot, name, netvm="", provides_network=False, dvm=False, tags=None, qid=60,
              default_dispvm="") -> Snapshot:
    from torii.live import QubeInfo
    s = copy.deepcopy(s)
    s.qubes[name] = QubeInfo(name, "AppVM", "Halted", "fedora-43-xfce", netvm, provides_network, qid, "", False,
                             "red", list(tags or []), {}, template_for_dispvms=dvm, default_dispvm=default_dispvm)
    return s


class Adoption(unittest.TestCase):
    """torii only changes qubes named in the file; it records what it found
    the first time, refuses moves it cannot judge, and anything leaving its
    control fails closed."""

    def test_empty_file_plans_nothing(self):
        self.assertEqual([str(s) for s in plan.make(mparse("qubes: {}"), snap(), VERSION)], [])

    def test_no_proxy_until_something_uses_one(self):
        steps = plan.make(mparse("qubes: {torii-probe: {network: none}}"), snap(), VERSION)
        self.assertEqual([str(s) for s in steps], [
            "torii-probe: tag torii-managed",
            "torii-probe: set feature torii-prior-netvm = none",
            f"torii-probe: set feature torii-adopted = {TODAY}",
        ])

    def test_first_adoption_records_the_netvm_once(self):
        claims.verifies(self, claims.SW_261127_VPGK, "prior-netvm-recorded-once")
        m = full_machine()
        steps = plan.make(m, snap(), VERSION)
        texts = [str(s) for s in steps]
        self.assertIn("personal: set feature torii-prior-netvm = sys-firewall", texts)
        self.assertIn("torii-probe: set feature torii-prior-netvm = none", texts)
        after = simulate(snap(), steps)
        self.assertEqual(after.qubes["personal"].features["torii-adopted"], TODAY)
        # Leaving and coming back keeps the first record.
        gone = simulate(after, plan.make(mparse("qubes: {}"), after, VERSION))
        self.assertEqual(gone.qubes["personal"].features["torii-prior-netvm"], "sys-firewall")
        back = plan.make(m, gone, VERSION)
        self.assertFalse([s for s in back if "torii-prior-netvm" in str(s)], back)

    def test_qube_adopted_before_records_existed_is_not_guessed(self):
        s = snap()
        s.qubes["personal"].tags.append(derive.MANAGED_TAG)
        s.qubes["personal"].netvm = "sys-torii"
        steps = plan.make(full_machine(), s, VERSION)
        self.assertFalse([x for x in steps if x.target == "personal" and "torii-prior" in str(x)])

    def test_leaving_the_file_fails_closed_and_names_the_old_netvm(self):
        claims.verifies(self, claims.SW_261127_VPGK, "leaving-fails-closed")
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        steps = plan.make(mparse("qubes: {torii-probe: {network: none}}"), after, VERSION)
        line = next(str(s) for s in steps if s.target == "personal" and s.kind == "pref")
        self.assertEqual(line, "personal: set netvm = (none) (was sys-torii; before torii: sys-firewall, "
                               "`qvm-prefs personal netvm sys-firewall` to restore)")
        self.assertEqual(simulate(after, steps).qubes["personal"].netvm, "")

    def test_main_proxy_is_kept_when_unused(self):
        after = simulate(snap(), plan.make(full_machine(), snap(), VERSION))
        steps = plan.make(mparse("qubes: {}"), after, VERSION)
        self.assertFalse([s for s in steps if s.kind == "remove"], steps)
        self.assertIn("sys-torii: unused (kept: it holds identities)", plan.notes(mparse("qubes: {}"), after))

    def test_unknown_netvm_needs_adopt_force(self):
        claims.verifies(self, claims.SW_261127_2JH3, "unknown-netvm-needs-force")
        s = with_qube(with_qube(snap(), "sys-whonix", "sys-firewall", provides_network=True, qid=61),
                      "anon-whonix", "sys-whonix")
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("qubes: {anon-whonix: {network: proxy}}"), s, VERSION)
        self.assertIn("anon-whonix: its netvm is sys-whonix, which torii doesn't know", str(e.exception))
        self.assertIn("adopt: force", str(e.exception))
        plan.make(mparse("qubes: {anon-whonix: {network: proxy, adopt: force}}"), s, VERSION)
        # Writing its netvm changes nothing, so nothing to refuse.
        plan.make(mparse("qubes: {anon-whonix: {network: sys-whonix}}"), s, VERSION)

    def test_known_netvms_are_adopted_without_force(self):
        s = with_qube(snap(), "w", "")  # no netvm
        plan.make(mparse("qubes: {w: {network: proxy}, personal: {network: proxy}}"), s, VERSION)  # sys-firewall: the default
        after = simulate(snap(), plan.make(full_machine(), snap(), VERSION))
        after = with_qube(after, "w2", "sys-torii")  # already behind a torii proxy
        plan.make(mparse("qubes: {w2: {network: none}}"), after, VERSION)

    def test_show_names_the_adoption(self):
        from torii import show
        after = simulate(snap(), plan.make(full_machine(), snap(), VERSION))
        v = show.qube_view(after, "personal", {})
        self.assertEqual(v["adopted"], {"from": "sys-firewall", "on": TODAY})
        self.assertIn(f"  adopted        from netvm sys-firewall on {TODAY}", show.format_view(dict(v, pattern="x")))

    def test_disposable_templates_need_disposables_yes(self):
        claims.verifies(self, claims.SW_261127_2JH3, "disposable-template-needs-yes")
        s = with_qube(snap(), "browser-dvm", "sys-firewall", dvm=True)
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("qubes: {browser-dvm: {network: proxy}}"), s, VERSION)
        self.assertIn("browser-dvm is a disposable template", str(e.exception))
        plan.make(mparse("qubes: {browser-dvm: {network: proxy, disposables: yes}}"), s, VERSION)
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("qubes: {personal: {network: proxy, disposables: yes}}"), s, VERSION)
        self.assertIn("personal is not a disposable template", str(e.exception))


EGRESS = """
egress:
  vpn: {exits: [exit-nyc, exit-ams], failback-after: 90s}
proxies: [{name: sys-torii-dvm, unlisted: {internet: all, egress: vpn}}]
qubes:
  browser: {network: proxy, egress: vpn, outbound: {internet: all}}
  build:
    egress: vpn
    outbound: {internet: [pypi.org], tailnet: yes, lan: [192.168.1.2:22]}
    tailnet: {identity: tagged, tags: [job]}
"""


class Egresses(unittest.TestCase):
    def bad(self, text, msg):
        with self.assertRaises(machine.FileError) as e:
            mparse(text)
        self.assertIn(msg, str(e.exception))

    def test_parse(self):
        m = mparse(EGRESS)
        g = m.egresses["vpn"]
        self.assertEqual((g.exits, g.failback_after, g.tags, g.hostname),
                         (("exit-nyc", "exit-ams"), 90, ("tag:m1-egress", "tag:m1"), "m1-egress-vpn"))
        self.assertEqual(mparse("egress: {e: {exits: [x]}}\nqubes: {}").egresses["e"].failback_after, 120)
        self.assertEqual(m.qubes["browser"].identity, "none", "an egress needs no identity of the qube's")
        self.assertEqual(m.egress_instances(), [("vpn", "sys-torii"), ("vpn", "sys-torii-dvm")])

    def test_errors(self):
        self.bad("qubes: {a: {network: proxy, egress: vpn, outbound: {internet: all}}}", "no egress 'vpn'")
        self.bad("egress: {vpn: {exits: []}}\nqubes: {}", "one or more exit nodes")
        self.bad("egress: {vpn: {exits: [a, a]}}\nqubes: {}", "an exit node twice")
        self.bad("egress: {vpn: {exits: [a], failback-after: soon}}\nqubes: {}", "a duration")
        self.bad("egress: {vpn: {exits: [a], via: b}}\nqubes: {}", "unknown key 'via'")
        self.bad("egress: {vpn: {exits: [a], probe: example.com}}\nqubes: {}", "host:port")
        self.bad("egress: {vpn: {exits: [a]}}\nqubes: {a: {network: proxy, egress: vpn}}", "outbound.internet: none")
        self.bad("egress: {vpn: {exits: [a]}}\nproxy: {name: sys-torii, unlisted: {internet: none}}\nqubes: {}",
                 "`unlisted: refuse`")
        self.bad("proxy: {name: sys-torii, unlisted: {internet: all, egress: nope}}\nqubes: {}", "no egress 'nope'")
        self.bad("egress: {vpn: {exits: [a]}}\nproxy: {name: sys-torii, unlisted: {internet: all, tailnet: yes}}\nqubes: {}",
                 "unknown key 'tailnet'")
        self.bad("egress: {vpn: {exits: [a]}}\nqubes: {a: {network: default, egress: vpn, outbound: {internet: all}}}",
                 "not carried by the proxy")
        # The egress's device name is a tailnet name like any other.
        self.bad("egress: {vpn: {exits: [a]}}\nqubes:\n  egress-vpn: {network: proxy, egress: vpn, outbound: {internet: all}, "
                 "tailnet: {identity: tagged, tags: [t]}}", "tailnet name m1-egress-vpn also used")

    def test_proxy_config(self):
        m = mparse(EGRESS)
        main = derive.proxy_config(m, "sys-torii", {"browser": "10.137.0.20"}, "v")
        self.assertEqual(main["egresses"], {"vpn": {
            "hostname": "m1-egress-vpn", "tags": ["tag:m1-egress", "tag:m1"], "exits": ["exit-nyc", "exit-ams"],
            "failback_after": 90, "auth_key_file": "/rw/torii/keys/egress-vpn"}})
        self.assertEqual(main["workloads"]["browser"]["egress"], "vpn")
        self.assertNotIn("identity", main["workloads"]["browser"])
        self.assertNotIn("unlisted", main, "the main proxy refuses unlisted qubes by default")
        dvm = derive.proxy_config(m, "sys-torii-dvm", {}, "v")
        self.assertEqual(dvm["egresses"]["vpn"]["hostname"], "m1-egress-vpn-sys-torii-dvm")
        self.assertEqual(dvm["unlisted"], {"internet": "full", "egress": "vpn"})
        self.assertEqual(dvm["workloads"], {})

    def test_grants_allow_only_the_internet(self):
        g = derive.grants(mparse(EGRESS))
        self.assertIn({"src": ["tag:m1-egress"], "dst": ["autogroup:internet"], "ip": ["*"]}, g["grants"])
        self.assertIn("tag:m1-egress", g["tagOwners"])
        self.assertFalse([x for x in g["grants"] if "tag:m1-egress" in x["src"] and x["dst"] != ["autogroup:internet"]])

    def test_unlisted_makes_a_proxy_used(self):
        m = mparse("egress: {vpn: {exits: [a]}}\nproxies: [{name: sys-torii-dvm, unlisted: {internet: all, egress: vpn}}]\nqubes: {}")
        steps = [str(s) for s in plan.make(m, snap(), VERSION)]
        self.assertTrue(any(s.startswith("sys-torii-dvm: create the proxy qube") for s in steps), steps)
        self.assertFalse(any(s.startswith("sys-torii: create") for s in steps), "the main proxy is not used")

    def test_lan_with_an_egress_is_noted(self):
        claims.verifies(self, claims.SW_261127_KYZW, "lan-noted")
        s = with_qube(snap(), "build", "")
        notes = plan.notes(mparse(EGRESS), s)
        self.assertIn("build: its LAN access goes direct through sys-torii's netvm, never through egress vpn", notes)


PROXY_STATUS = {"status": {
    "config_version": "v", "proxy_qube": "sys-torii", "workloads": {},
    "egresses": {"vpn": {
        "name": "vpn", "active": "exit-ams", "active_since": "2026-11-08T10:02:00Z", "usable": True,
        "exits": [{"name": "exit-nyc", "healthy": False, "why": "ping: missed 3"},
                  {"name": "exit-ams", "healthy": True, "healthy_since": "2026-11-08T10:00:00Z"}],
        "identity": {"hostname": "m1-egress-vpn", "state": "NeedsLogin", "auth_url": "https://login/x"}}}},
    "log": [
        {"workload": "-", "direction": "out", "source": "10.137.0.50", "dest": "1.1.1.1:443", "verdict": "refuse",
         "reason": "unknown-workload"},
        {"workload": "-", "direction": "dns", "source": "10.137.0.50", "dest": "a.org A", "verdict": "refuse",
         "reason": "unknown-workload"},
        {"workload": "unlisted:10.137.0.51", "direction": "out", "source": "unlisted:10.137.0.51",
         "dest": "1.1.1.1:443", "verdict": "allow", "reason": "internet-full"},
        {"workload": "egress:vpn", "direction": "egress", "dest": "exit-ams", "reason": "exit-switch"}]}


class Disposables(unittest.TestCase):
    """A disposable takes its netvm from its disposable template, not from
    the qube that opened it: a qube forced through an egress can still open
    a disposable that goes out direct."""

    def test_escape_through_default_dispvm(self):
        from torii import show
        m = mparse(EGRESS)
        after = simulate(snap(), plan.make(full_machine(), snap(), VERSION))  # sys-torii exists, owned
        s = with_qube(after, "default-dvm", "sys-firewall", dvm=True, qid=70)
        s = with_qube(s, "browser-dvm", "sys-torii", dvm=True, qid=71)
        s = with_qube(s, "offline-dvm", "", dvm=True, qid=72)
        for name, dvm in (("browser", "default-dvm"), ("build", "offline-dvm")):
            s = with_qube(s, name, "", default_dispvm=dvm, qid=80 + len(name))
        self.assertEqual(show.dispvm_escapes(m, s), [
            "browser uses egress vpn, but its default_dispvm (default-dvm) has netvm sys-firewall: "
            "disposables opened from browser go out directly"])
        s.qubes["browser"].default_dispvm = "browser-dvm"
        self.assertEqual(show.dispvm_escapes(m, s), [], "a disposable template behind a torii proxy")


class EgressStatus(unittest.TestCase):
    def test_sign_in_links_include_egresses(self):
        from torii import status
        self.assertEqual(status.sign_in_links(PROXY_STATUS), {"egress vpn": "https://login/x"})

    def test_show_egress(self):
        from torii import show
        self.assertEqual(show.egress_lines(PROXY_STATUS["status"]), [
            "  egress vpn: active exit-ams since 2026-11-08 10:02 UTC",
            "    exit-nyc  down (ping: missed 3)",
            "    exit-ams  up",
        ])
        down = copy.deepcopy(PROXY_STATUS["status"])
        down["egresses"]["vpn"]["usable"] = False
        self.assertEqual(show.egress_lines(down)[0], "  egress vpn: NO HEALTHY EXIT NODE (connections refused; last exit-ams)")

    def test_unlisted_sources_are_named_by_dom0(self):
        from torii import show
        s = with_qube(with_qube(snap(), "shopping", "sys-torii", qid=50), "disp1234", "sys-torii", qid=51)
        self.assertEqual(s.qubes["shopping"].address, "10.137.0.50")
        self.assertEqual(show.unlisted_lines(s, PROXY_STATUS), [
            "  unlisted disp1234 (10.137.0.51): 1 allowed, 0 refused",
            "  unlisted shopping (10.137.0.50): 0 allowed, 2 refused",
        ])

    def test_check_egress(self):
        from torii import show
        import datetime
        now = datetime.datetime(2026, 11, 10, 12, 0, tzinfo=datetime.timezone.utc)
        self.assertEqual(show.egress_problems("sys-torii", PROXY_STATUS["status"], now), [
            "egress vpn on sys-torii: identity m1-egress-vpn is NeedsLogin; sign in: https://login/x",
            "egress vpn on sys-torii: on fallback exit-ams since 2026-11-08 10:02 UTC (exit-nyc: ping: missed 3)",
        ])
        st = copy.deepcopy(PROXY_STATUS["status"])
        st["egresses"]["vpn"].update(usable=False)
        st["egresses"]["vpn"]["identity"]["state"] = "Running"
        self.assertEqual(show.egress_problems("sys-torii", st, now),
                         ["egress vpn on sys-torii: no healthy exit node; its qubes' internet is refused "
                          "(exit-nyc: ping: missed 3)"])


class Chunks(unittest.TestCase):
    def test_roundtrip_and_qubesdb_limit(self):
        claims.verifies(self, claims.SW_261127_XGDY, "chunks-roundtrip")
        m = full_machine()
        big = {"workloads": {f"q{i}": {"internet_allow": [f"h{i}-{j}.example.org" for j in range(40)]}
                             for i in range(40)}}
        for cfg in (derive.proxy_config(m, "sys-torii", {}, "v"), big):
            f = derive.encode_chunks(cfg)
            self.assertTrue(all(len(v) < 3072 for v in f.values()))
            self.assertEqual(derive.decode_chunks(f), json.loads(json.dumps(cfg)))
        self.assertGreater(len(derive.encode_chunks(big)), 2)

    def test_half_written_is_refused(self):
        claims.verifies(self, claims.SW_261127_XGDY, "half-written-refused")
        big = {"x": [os.urandom(8).hex() for _ in range(2000)]}
        f = derive.encode_chunks(big)
        f[f"{derive.CONFIG_FEATURE}.1"] = f[f"{derive.CONFIG_FEATURE}.1"][::-1]
        with self.assertRaises(Exception):
            derive.decode_chunks(f)
        # A new config's parts, all of them, under the old head: every part
        # decodes, and only the hash tells the two writes apart.
        old = derive.encode_chunks({"version": "v1", "x": "a" * 10})
        new = derive.encode_chunks({"version": "v2", "x": "b" * 10})
        self.assertEqual(old[derive.CONFIG_FEATURE].split()[0], new[derive.CONFIG_FEATURE].split()[0])
        mixed = dict(new, **{derive.CONFIG_FEATURE: old[derive.CONFIG_FEATURE]})
        with self.assertRaisesRegex(ValueError, "do not match their hash"):
            derive.decode_chunks(mixed)



CALLS = """
qubes:
  job:
    outbound: {internet: [pypi.org]}
  silent:
    tailnet: {identity: tagged, tags: [s], inbound: [{port: 443, target: 8443}]}
  peer:
    local-inbound: [{port: 22, from: [job]}, {port: 80, from: [silent]}]
"""


def allows(text: str) -> list[tuple[str, str, str, str]]:
    """The policy's allow lines as (service, argument, source, target)."""
    out = []
    for line in text.splitlines():
        f = line.split()
        if len(f) >= 5 and not line.startswith("#") and f[4] == "allow":
            out.append((f[0], f[1], f[2], f[3]))
    return out


class Calls(unittest.TestCase):
    """The policy apply writes allows each call in the direction of its
    connection, and only the calls the file declares."""

    def setUp(self):
        self.m = mparse(CALLS)
        self.lines = allows(derive.policy(self.m))

    def test_outbound_goes_to_its_own_proxy(self):
        claims.verifies(self, claims.SW_261127_M36P, "outbound-own-proxy-only")
        out = [l for l in self.lines if l[0] == "torii.Connect"]
        self.assertEqual({l[2] for l in out}, {"job"}, "only the qube declaring outbound access calls out")
        self.assertEqual({l[3] for l in out}, {"@default", "sys-torii"})
        self.assertEqual({l[1] for l in out}, {"+socks", "+http"})
        for line in derive.policy(self.m).splitlines():
            if line.startswith("torii.Connect") and "@default" in line:
                self.assertTrue(line.endswith("allow target=sys-torii"), line)

    def test_inbound_only_on_listed_ports(self):
        claims.verifies(self, claims.SW_261127_M36P, "inbound-listed-ports")
        into = [l for l in self.lines if l[0] == "qubes.ConnectTCP" and l[3] == "silent"]
        self.assertEqual(into, [("qubes.ConnectTCP", "+8443", "sys-torii", "silent")])
        local = [l for l in self.lines if l[0] == "qubes.ConnectTCP" and l[3] == "peer"]
        self.assertEqual(sorted(local), [("qubes.ConnectTCP", "+22", "job", "peer"), ("qubes.ConnectTCP", "+80", "silent", "peer")])

    def test_a_qube_with_no_outbound_makes_no_call(self):
        claims.verifies(self, claims.SW_261127_M36P, "silent-qube-no-calls")
        self.assertEqual([l for l in self.lines if l[2] == "peer"], [])
        self.assertEqual([l for l in self.lines if l[2] == "silent"], [("qubes.ConnectTCP", "+80", "silent", "peer")])

    def test_every_other_caller_is_denied(self):
        claims.verifies(self, claims.SW_261127_M36P, "others-denied")
        deny = derive.deny_policy().splitlines()
        self.assertIn("torii.Connect  *  @anyvm  @anyvm  deny", deny)
        self.assertIn("torii.Status   *  @anyvm  @anyvm  deny", deny)


class Owned(unittest.TestCase):
    """apply changes only what torii owns."""

    def test_only_owned_settings_change(self):
        claims.verifies(self, claims.SW_261127_G26T, "fresh-machine-steps")
        m = full_machine()
        s = snap()
        own = {s.target for s in plan.make(m, s, VERSION) if s.kind in ("create", "clone", "template-install")}
        own |= set(m.proxies) | {m.template, derive.policy_name(m), derive.DENY_POLICY}
        listed = set(m.qubes)
        owned_features = {derive.CLIENT_FEATURE, "service." + derive.CLIENT_SERVICE, derive.PRIOR_NETVM_FEATURE, derive.ADOPTED_FEATURE}
        for st in plan.make(m, s, VERSION):
            if st.target in own:
                continue
            self.assertIn(st.target, listed, f"{st} changes a qube that is neither torii's nor listed")
            if st.kind == "pref":
                self.assertEqual(st.data["key"], "netvm", str(st))
            elif st.kind in ("feature-set", "feature-del"):
                self.assertIn(st.data["key"], owned_features, str(st))
            elif st.kind in ("tag-add", "tag-del"):
                self.assertIn(st.data["tag"], (derive.MANAGED_TAG, derive.KEEP_TAG), str(st))
            else:
                self.fail(f"{st}: a {st.kind} step on a workload")

    def test_proxies_have_ipv6_off(self):
        claims.verifies(self, claims.SW_261127_3Q23, "ipv6-off")
        m = full_machine()
        fresh = [st for st in plan.make(m, snap(), VERSION) if st.target == "sys-torii" and st.kind == "feature-set"
                 and st.data["key"] == "ipv6"]
        self.assertEqual([st.data["value"] for st in fresh], [""])
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        after.qubes["sys-torii"].features["ipv6"] = "1"
        again = [st for st in plan.make(m, after, VERSION) if st.kind == "feature-set" and st.data["key"] == "ipv6"]
        self.assertEqual([(st.target, st.data["value"]) for st in again], [("sys-torii", "")])

    def test_the_bundle_is_installed_once(self):
        claims.verifies(self, claims.SW_261127_HSHY, "bundle-installs")
        m = full_machine()
        steps = plan.make(m, snap(), VERSION)
        pkg = [st for st in steps if st.kind == "template-package"]
        self.assertEqual([(st.target, st.data["version"]) for st in pkg], [(m.template, VERSION)])
        after = simulate(snap(), steps)
        self.assertFalse([st for st in plan.make(m, after, VERSION) if st.kind == "template-package"])


class Resume(unittest.TestCase):
    def test_a_stopped_apply_continues(self):
        claims.verifies(self, claims.SW_261127_63CS, "second-run-continues")
        m = full_machine()
        steps = plan.make(m, snap(), VERSION)
        for k in (1, len(steps) // 2, len(steps) - 1):
            partial = simulate(snap(), steps[:k])
            rest = plan.make(m, partial, VERSION)
            done = {str(st) for st in steps[:k]}
            self.assertFalse(done & {str(st) for st in rest}, f"after {k} steps the plan repeats one")
            self.assertEqual(plan.make(m, simulate(partial, rest), VERSION), [], f"after {k} steps")


class FakeFeatures(dict):
    def __init__(self, log):
        super().__init__()
        self.log = log

    def __setitem__(self, k, v):
        self.log.append(("set", k))
        super().__setitem__(k, v)

    def __delitem__(self, k):
        self.log.append(("del", k))
        super().__delitem__(k)


class FakeProc:
    def __init__(self, out):
        self.out, self.returncode = out, 0

    def communicate(self, data, timeout=None):
        return self.out, b""


class FakeVM:
    def __init__(self, name, versions=()):
        self.name, self.log = name, []
        self.features = FakeFeatures(self.log)
        self.running = False
        self.versions = list(versions)  # what torii.Status reports, call by call

    def is_running(self):
        return self.running

    def start(self):
        self.log.append(("start", self.name))
        self.running = True

    def run_service(self, name, user=None):
        v = self.versions.pop(0) if len(self.versions) > 1 else (self.versions[0] if self.versions else None)
        return FakeProc(json.dumps({"status": {"config_version": v}}).encode())


class FakeApp:
    def __init__(self, *vms):
        self.domains = {v.name: v for v in vms}


class Applying(unittest.TestCase):
    """apply's runner, against a fake Admin API."""

    def applier(self, *vms, timeout=5):
        from torii import apply as ap
        out = []
        real = ap.time.sleep
        ap.time.sleep = lambda s: None
        self.addCleanup(setattr, ap.time, "sleep", real)
        return ap.Applier(FakeApp(*vms), None, "/nonexistent", out=out.append, ready_timeout=timeout), out

    def test_stops_at_the_first_failure(self):
        claims.verifies(self, claims.SW_261127_63CS, "stops-at-first-failure")
        from torii import apply as ap
        vm = FakeVM("w")
        a, out = self.applier(vm)
        steps = [plan.Step("feature-set", "w", "one", {"key": "a", "value": "1"}),
                 plan.Step("feature-set", "nosuch", "two", {"key": "b", "value": "2"}),
                 plan.Step("feature-set", "w", "three", {"key": "c", "value": "3"})]
        with self.assertRaises(ap.ApplyError) as e:
            a.run(steps)
        self.assertIs(e.exception.step, steps[1])
        self.assertIn("nosuch: two", str(e.exception))
        self.assertEqual(dict(vm.features), {"a": "1"}, "a step after the failure was made")
        self.assertEqual(out, ["[1/3] w: one", "[2/3] nosuch: two"])

    def test_config_parts_first_head_last(self):
        claims.verifies(self, claims.SW_261127_XGDY, "head-written-last")
        vm = FakeVM("p")
        a, _ = self.applier(vm)
        head = derive.CONFIG_FEATURE
        vm.features.update({f"{head}.3": "stale"})
        feats = dict(derive.encode_chunks({"x": [os.urandom(8).hex() for _ in range(2000)]}))
        feats[f"{head}.3"] = None
        a.do_proxy_config(plan.Step("proxy-config", "p", "c", {"features": feats}))
        order = [k for op, k in vm.log]
        hi = order.index(head)
        self.assertTrue(all(order.index(k) < hi for k in feats if k != head and feats[k] is not None), order)
        self.assertEqual(vm.log[-1], ("del", f"{head}.3"))
        self.assertNotIn(f"{head}.3", vm.features)

    def test_waits_until_the_proxy_runs_the_version(self):
        claims.verifies(self, claims.SW_261127_QMVP, "waits-for-version")
        vm = FakeVM("p", versions=[None, "v1", "v1", "v2"])
        a, _ = self.applier(vm)
        a.do_proxy_ready(plan.Step("proxy-ready", "p", "r", {"version": "v2", "package": VERSION}))
        self.assertEqual(vm.versions, ["v2"], "returned before the proxy reported v2")
        self.assertIn(("start", "p"), vm.log)

    def test_times_out_naming_what_it_runs(self):
        claims.verifies(self, claims.SW_261127_QMVP, "times-out-naming-state")
        from torii import apply as ap
        vm = FakeVM("p", versions=["v1"])
        vm.running = True
        a, _ = self.applier(vm, timeout=0.05)
        with self.assertRaises(ap.ApplyError) as e:
            a.do_proxy_ready(plan.Step("proxy-ready", "p", "r", {"version": "v2", "package": VERSION}))
        self.assertIn("runs config v1", str(e.exception))


NOW = __import__("datetime").datetime(2026, 11, 27, tzinfo=__import__("datetime").timezone.utc)


def applied():
    """The full machine after apply, with its proxy running the config its
    features hold."""
    m = full_machine()
    after = simulate(snap(), plan.make(m, snap(), VERSION))
    cfg = derive.decode_chunks(after.qubes["sys-torii"].features)
    ps = {"sys-torii": {"config": cfg, "status": {"status": {"config_version": cfg["version"], "workloads": {}}, "log": []}}}
    return m, after, ps


class Show(unittest.TestCase):
    """show reads the live machine, never the file."""

    def test_hand_written_policy_shows(self):
        claims.verifies(self, claims.SW_261127_62WV, "hand-policy-shown")
        m, after, ps = applied()
        after.other_policy["30-mine"] = "qubes.ConnectTCP  +22  torii-probe  personal  allow\n"
        v = show.qube_view(after, "personal", ps)
        self.assertIn({"from": "torii-probe", "port": "22", "policy": "30-mine"}, v["local_inbound"])
        # From any qube, and on a prompt: shown as reach, the prompt named.
        after.other_policy["31-any"] = "qubes.ConnectTCP  +2222  @anyvm  personal  ask\n"
        v = show.qube_view(after, "personal", ps)
        self.assertIn({"from": "torii-probe2", "port": "2222", "policy": "31-any (ask)"}, v["local_inbound"])
        # A file that names no service of torii's, by the wildcard service.
        after.other_policy["32-all"] = "*  *  torii-probe2  personal  allow\n"
        v = show.qube_view(after, "personal", ps)
        self.assertIn({"from": "torii-probe2", "port": "*", "policy": "32-all"}, v["local_inbound"])

    def test_the_first_matching_rule_decides(self):
        claims.verifies(self, claims.SW_261127_62WV, "first-match-wins")
        m, after, ps = applied()
        after.other_policy["20-deny"] = "qubes.ConnectTCP  +22  torii-probe  personal  deny\n"
        after.other_policy["30-mine"] = "qubes.ConnectTCP  +22  torii-probe  personal  allow\n"
        v = show.qube_view(after, "personal", ps)
        self.assertEqual([x for x in v["local_inbound"] if x["from"] == "torii-probe"], [])
        after.other_policy["30-mine"] = "qubes.ConnectTCP  *  torii-probe  personal  allow\n"
        del after.other_policy["20-deny"]
        v = show.qube_view(after, "personal", ps)
        self.assertIn({"from": "torii-probe", "port": "*", "policy": "30-mine"}, v["local_inbound"])
        # A redirect decides for the target asked for, and reaches only its
        # new target.
        after.other_policy = {"20-redirect": "qubes.ConnectTCP  +22  torii-probe  personal  allow target=work\n",
                              "30-mine": "qubes.ConnectTCP  +22  torii-probe  personal  allow\n"}
        v = show.qube_view(after, "personal", ps)
        self.assertEqual([x for x in v["local_inbound"] if x["from"] == "torii-probe"], [])

    def test_the_live_netvm_is_shown(self):
        claims.verifies(self, claims.SW_261127_62WV, "live-netvm-shown")
        m, after, ps = applied()
        after.qubes["personal"].netvm = "sys-firewall"
        v = show.qube_view(after, "personal", ps)
        self.assertEqual((v["netvm"], v["link"]), ("sys-firewall", "netvm"))
        self.assertEqual(v["internet"], "as sys-firewall allows (qvm-firewall)")


class Check(unittest.TestCase):
    """check names every difference, and nothing on a matching machine."""

    def test_drift_is_named(self):
        claims.verifies(self, claims.SW_261127_SMYC, "drift-named")
        m, after, ps = applied()
        after.qubes["personal"].netvm = "sys-firewall"
        self.assertIn("drift: personal: set netvm = sys-torii (was sys-firewall)",
                      show.check_problems(m, after, ps, VERSION, NOW))

    def test_a_stale_config_is_named(self):
        claims.verifies(self, claims.SW_261127_SMYC, "stale-config-named")
        m, after, ps = applied()
        want = ps["sys-torii"]["config"]["version"]
        ps["sys-torii"]["status"]["status"]["config_version"] = "old"
        self.assertIn(f"sys-torii: runs config old, its features hold {want}",
                      show.check_problems(m, after, ps, VERSION, NOW))

    def test_widening_policy_is_named(self):
        claims.verifies(self, claims.SW_261127_SMYC, "widening-policy-named")
        m, after, ps = applied()
        after.other_policy["30-mine"] = "torii.Connect  +socks  work  sys-torii  allow\n"
        self.assertIn("policy 30-mine: also allows torii.Connect +socks work -> sys-torii",
                      show.check_problems(m, after, ps, VERSION, NOW))
        # A file named like torii's but not this mesh's is not exempt.
        after.other_policy = {"50-torii-evil": "torii.Connect  +socks  work  sys-torii  allow\n",
                              "31-all": "*  *  work  sys-torii  allow\n",
                              "32-ask": "qubes.ConnectTCP  +22  @anyvm  personal  ask\n",
                              "33-tag": "qubes.ConnectTCP  +22  @tag:x  personal  allow\n",
                              "34-inc": "!include  include/more\n",
                              derive.DENY_POLICY: derive.deny_policy()}
        got = show.check_problems(m, after, ps, VERSION, NOW)
        for want in ("policy 50-torii-evil: also allows torii.Connect +socks work -> sys-torii",
                     "policy 31-all: also allows * * work -> sys-torii",
                     "policy 32-ask: may allow, on a prompt, qubes.ConnectTCP +22 @anyvm -> personal",
                     "policy 33-tag: also allows qubes.ConnectTCP +22 @tag:x -> personal (a token check does not evaluate)",
                     "policy 34-inc: includes include/more, which check does not read"):
            self.assertIn(want, got)

    def test_a_matching_machine_has_no_problem(self):
        claims.verifies(self, claims.SW_261127_SMYC, "clean-is-zero")
        m, after, ps = applied()
        self.assertEqual(show.check_problems(m, after, ps, VERSION, NOW), [])


if __name__ == "__main__":
    unittest.main()
