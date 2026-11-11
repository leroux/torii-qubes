"""Golden and property tests for the dom0 tool. Run: python3 -m pytest tests
(or python3 -m unittest discover tests). UPDATE_GOLDEN=1 rewrites goldens."""

import copy
import json
import os
import unittest

from lintel import derive, machine, plan


def mparse(text: str):
    """machine.parse with the machine's name added when a test leaves it out,
    and `defaults: {network: none}` when it has no defaults (network is
    required)."""
    if "defaults:" not in text:
        text = "defaults: {network: none}\n" + text
    return machine.parse(text if "machine:" in text else "machine: m1\n" + text)


from lintel.live import Snapshot

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
    from lintel.live import QubeInfo
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
            q.features["lintel-version"] = st.data["version"]
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
        p = m.qubes["lintel-probe"]
        self.assertEqual((p.network_card, p.internet, p.identity, p.tags), (False, "none", "tagged", ("tag:m1-lintel-test", "tag:m1")))
        self.assertEqual(p.inbound[1], machine.Inbound(8443, 8080))
        self.assertEqual((p.socks_port, p.http_port), (0, 0), "no outbound, no proxy ports")
        j = m.qubes["lintel-probe2"]
        self.assertEqual((j.socks_port, j.http_port, j.tags), (1080, 3128, ("tag:m1-lintel-job", "tag:m1")))
        r = m.qubes["personal"]
        self.assertEqual((r.link, r.identity, r.tags, r.socks_port), ("routed", "login", (), 0))

    def test_example_file_parses(self):
        machine.load(os.path.join(HERE, "..", "..", "examples", "machine.yaml"))
        machine.load(os.path.join(HERE, "..", "..", "proxy", "internal", "engine", "testdata", "sim", "machine.yaml"))

    def test_every_machine_file_in_the_docs_parses(self):
        """Every ```yaml block in the README and docs/ parses with the tool's
        own parser. An indented block is a fragment of qubes: and is parsed
        under it. Blocks that are not machine files (none yet) would be
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
                doc = ("qubes:\n" + block) if first.startswith(" ") else block
                with self.subTest(file=os.path.basename(path), block=i):
                    try:
                        mparse("defaults: {network: none, tailnet: {tags: [t]}}\n" + doc if "defaults:" not in doc else doc)
                    except machine.FileError as e:
                        self.fail(f"{os.path.basename(path)} yaml block {i}: {e}\n{block}")
                n += 1
        self.assertGreater(n, 3, "found the docs' yaml blocks")

    def test_errors(self):
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
        self.bad("qubes: {a: {outbound: {via: ex, internet: all}}}", "needs an identity")
        self.bad("qubes: {sys-lintel: {}}", "is a proxy qube")
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
        cfg = derive.proxy_config(m, "sys-lintel", {}, "v")["workloads"]
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
        self.bad("qubes: {a: {internet: full}}", "moved to outbound.internet")
        self.bad("qubes: {a: {internet-allow: [x.org]}}", "moved to the list in outbound.internet")
        self.bad("qubes: {a: {internet-via: ex}}", "moved to outbound.via")
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
        cfg = derive.proxy_config(m, "sys-lintel", {}, "v")["workloads"]
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
        cfg = derive.proxy_config(full_machine(), "sys-lintel", addrs, "v1")
        golden("proxy-config.json", json.dumps(cfg, indent=1, sort_keys=True) + "\n")

    def test_second_apply_changes_nothing(self):
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
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        after.qubes["personal"].state = "Running"
        steps = plan.make(m, after, VERSION + ".1")
        self.assertEqual([str(s) for s in steps], [
            "lintel-fedora-44-minimal: install the proxy package 0.1.0-test.1 (has 0.1.0-test)",
            "sys-lintel: restart it on the new package (detaching and reattaching personal)",
        ])

    def test_proxy_started_on_an_older_package_is_restarted(self):
        """Another mesh's apply upgraded the template they share: this
        mesh's running proxy still runs the old package until it restarts,
        though the template has nothing left to install."""
        m = full_machine()
        old = simulate(snap(), plan.make(m, snap(), VERSION))
        old.qubes["sys-lintel"].state = "Running"
        old.qubes[m.template].features["lintel-version"] = VERSION + ".1"  # by the other mesh
        steps = plan.make(m, old, VERSION + ".1")
        self.assertEqual([str(s) for s in steps], [
            "sys-lintel: restart it on the new package",
        ])
        self.assertEqual(plan.make(m, simulate(old, steps), VERSION + ".1"), [])

    def test_new_base_template_moves_the_proxies(self):
        """A machine whose file named another template (Debian, lintel-template)
        moves to the new base with plain apply: a new template, each proxy
        moved onto it with one restart, the old template removed."""
        with open(os.path.join(G, "full.yaml"), encoding="utf-8") as f:
            text = f.read()
        old_m = mparse("template: lintel-template\nbase-template: debian-13-minimal\n" + text)
        before = simulate(snap(), plan.make(old_m, snap(), VERSION))
        before.qubes["sys-lintel"].state = "Running"
        before.qubes["personal"].state = "Running"
        m = full_machine()
        steps = plan.make(m, before, VERSION)
        self.assertEqual([str(s) for s in steps], [
            "fedora-44-minimal: download the base template (qvm-template install)",
            "lintel-fedora-44-minimal: create the proxy template, a clone of fedora-44-minimal",
            "lintel-fedora-44-minimal: install the proxy package 0.1.0-test",
            "sys-lintel: move it to lintel-fedora-44-minimal and restart it (detaching and reattaching personal)",
            "lintel-template: remove the tool's old template (nothing uses it now; lintel-fedora-44-minimal replaces it)",
        ])
        after = simulate(before, steps)
        self.assertEqual(after.qubes["sys-lintel"].template, "lintel-fedora-44-minimal")
        self.assertNotIn("lintel-template", after.qubes)
        self.assertIn("debian-13-minimal", after.qubes, "the base template is the person's, not the tool's")
        self.assertEqual(plan.make(m, after, VERSION), [])

    def test_old_template_another_mesh_uses_stays(self):
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        after.qubes["lintel-template"] = copy.deepcopy(after.qubes["lintel-fedora-44-minimal"])
        after.qubes["lintel-template"].name = "lintel-template"
        other = copy.deepcopy(after.qubes["sys-lintel"])
        other.name, other.template = "sys-lintel-other", "lintel-template"
        other.tags = [derive.OWNED_TAG, derive.MESH_TAG + "other"]  # another mesh's proxy
        after.qubes["sys-lintel-other"] = other
        self.assertEqual(plan.make(m, after, VERSION), [])

    def test_services_on_a_qube_the_file_does_not_manage(self):
        m = mparse(SERVICES)
        steps = plan.make(m, snap(), VERSION)
        golden("plan-services.txt", "\n".join(str(s) for s in steps) + "\n")
        self.assertFalse([s for s in steps if s.target == "personal"], "its network is not the tool's")
        cfg = derive.proxy_config(m, "sys-lintel", {}, "v")
        self.assertEqual(sorted(cfg["workloads"]), ["personal.chat", "personal.notes"])
        self.assertEqual(cfg["workloads"]["personal.chat"]["inbound"], [{"port": 443, "target": 8008, "tls": True}])
        self.assertIn("qubes.ConnectTCP  +8008  sys-lintel  personal  allow", derive.policy(m))
        self.assertIn("tag:chat", derive.grants(m)["tagOwners"])
        # Dropping the services leaves the qube's netvm as it was.
        after = simulate(snap(), steps)
        gone = plan.make(mparse("qubes: {}"), after, VERSION)
        self.assertFalse([s for s in gone if s.target == "personal"], gone)
        self.assertEqual(simulate(after, gone).qubes["personal"].netvm, "sys-firewall")

    def test_network_values(self):
        m = mparse("""
proxies: [{name: sys-lintel-vpn, netvm: sys-net}]
qubes:
  a: {network: none}
  b: {network: proxy}
  c: {network: proxy, proxy: sys-lintel-vpn, outbound: {internet: all}}
  d: {network: default}
  e: {network: sys-net}
""")
        st = {n: derive.qube_state(m, q).netvm for n, q in m.qubes.items()}
        self.assertEqual(st, {"a": "", "b": "sys-lintel", "c": "sys-lintel-vpn", "d": derive.DEFAULT_NETVM, "e": "sys-net"})
        self.assertEqual(m.qubes["c"].proxy, "sys-lintel-vpn")

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
        main = mparse("qubes: {lintel-probe: {}, personal: {network: proxy, outbound: {internet: all}}}")
        test = mparse("""
mesh: test
machine: m1-test
proxy: {name: sys-lintel-test}
qubes: {lintel-probe2: {tailnet: {identity: tagged, tags: [t]}}}
""")
        snap1 = simulate(snap(), plan.make(main, snap(), VERSION))
        steps = plan.make(test, as_mesh(snap1, test), VERSION)
        golden("plan-second-mesh.txt", "\n".join(str(s) for s in steps) + "\n")
        targets = {s.target for s in steps}
        self.assertFalse(targets & {"lintel-probe", "personal", "sys-lintel", "50-lintel"}, "the test mesh touched the main mesh")
        self.assertIn("50-lintel-test", targets)
        self.assertEqual(derive.policy_name(test), "50-lintel-test")
        snap2 = simulate(as_mesh(snap1, test), steps)
        created = snap2.qubes["sys-lintel-test"]
        self.assertEqual(set(created.tags) >= {derive.OWNED_TAG, "lintel-mesh-test"}, True)
        # Each mesh's next plan is empty: neither undoes the other.
        self.assertEqual(plan.make(main, as_mesh(snap2, main), VERSION), [])
        self.assertEqual(plan.make(test, as_mesh(snap2, test), VERSION), [])
        # A qube managed by one mesh cannot be claimed by the other.
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mpatched(test, "lintel-probe"), as_mesh(snap2, test), VERSION)
        self.assertIn("managed by another mesh", str(e.exception))
        # A mesh's own proxy name cannot be taken by another mesh.
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("mesh: other\nproxy: {name: sys-lintel-test}\nqubes: {}"), snap2, VERSION)
        self.assertIn("another mesh", str(e.exception))

    def test_workload_must_exist(self):
        m = mparse("defaults: {network: none, tailnet: {tags: [t]}}\nqubes: {nosuch: {}}")
        with self.assertRaises(plan.PlanError) as e:
            plan.make(m, snap(), VERSION)
        self.assertIn("never creates workloads", str(e.exception))

    def test_netvms_are_not_workloads(self):
        with self.assertRaises(plan.PlanError):
            plan.make(mparse("qubes: {sys-net: {}}"), snap(), VERSION)

    def test_foreign_template_name(self):
        with self.assertRaises(plan.PlanError):
            plan.make(mparse("template: fedora-43-xfce\nqubes: {personal: {network: proxy}}"), snap(), VERSION)


class Schema(unittest.TestCase):
    """The file says each fact once: a qube's proxy in proxy:, the kind of
    identity in tailnet.identity, its network explicitly."""

    def bad(self, text, msg):
        with self.assertRaises(machine.FileError) as e:
            mparse(text)
        self.assertIn(msg, str(e.exception))

    def test_network_is_required(self):
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
        self.assertEqual({n: q.proxy for n, q in m.qubes.items()}, {"a": "p2", "b": "p2", "c": "sys-lintel"})
        self.assertEqual(derive.qube_state(m, m.qubes["b"]).netvm, "p2")
        self.bad("qubes: {a: {proxy: sys-lintel-x}}", "no proxy 'sys-lintel-x'")

    def test_adopt_and_disposables_keys(self):
        m = mparse("qubes: {a: {adopt: force}, b: {disposables: yes}, c: {}}")
        self.assertEqual([(q.adopt_force, q.disposables) for q in m.qubes.values()],
                         [(True, False), (False, True), (False, False)])
        self.bad("qubes: {a: {adopt: yes}}", "adopt: force")
        self.bad("defaults: {network: none, adopt: force}\nqubes: {}", "unknown key 'adopt'")


def with_qube(s: Snapshot, name, netvm="", provides_network=False, dvm=False, tags=None, qid=60) -> Snapshot:
    from lintel.live import QubeInfo
    s = copy.deepcopy(s)
    s.qubes[name] = QubeInfo(name, "AppVM", "Halted", "fedora-43-xfce", netvm, provides_network, qid, "", False,
                             "red", list(tags or []), {}, template_for_dispvms=dvm)
    return s


class Adoption(unittest.TestCase):
    """lintel only changes qubes named in the file; it records what it found
    the first time, refuses moves it cannot judge, and anything leaving its
    control fails closed."""

    def test_empty_file_plans_nothing(self):
        self.assertEqual([str(s) for s in plan.make(mparse("qubes: {}"), snap(), VERSION)], [])

    def test_no_proxy_until_something_uses_one(self):
        steps = plan.make(mparse("qubes: {lintel-probe: {network: none}}"), snap(), VERSION)
        self.assertEqual([str(s) for s in steps], [
            "lintel-probe: tag lintel-managed",
            "lintel-probe: set feature lintel-prior-netvm = none",
            f"lintel-probe: set feature lintel-adopted = {TODAY}",
        ])

    def test_first_adoption_records_the_netvm_once(self):
        m = full_machine()
        steps = plan.make(m, snap(), VERSION)
        texts = [str(s) for s in steps]
        self.assertIn("personal: set feature lintel-prior-netvm = sys-firewall", texts)
        self.assertIn("lintel-probe: set feature lintel-prior-netvm = none", texts)
        after = simulate(snap(), steps)
        self.assertEqual(after.qubes["personal"].features["lintel-adopted"], TODAY)
        # Leaving and coming back keeps the first record.
        gone = simulate(after, plan.make(mparse("qubes: {}"), after, VERSION))
        self.assertEqual(gone.qubes["personal"].features["lintel-prior-netvm"], "sys-firewall")
        back = plan.make(m, gone, VERSION)
        self.assertFalse([s for s in back if "lintel-prior-netvm" in str(s)], back)

    def test_qube_adopted_before_records_existed_is_not_guessed(self):
        s = snap()
        s.qubes["personal"].tags.append(derive.MANAGED_TAG)
        s.qubes["personal"].netvm = "sys-lintel"
        steps = plan.make(full_machine(), s, VERSION)
        self.assertFalse([x for x in steps if x.target == "personal" and "lintel-prior" in str(x)])

    def test_leaving_the_file_fails_closed_and_names_the_old_netvm(self):
        m = full_machine()
        after = simulate(snap(), plan.make(m, snap(), VERSION))
        steps = plan.make(mparse("qubes: {lintel-probe: {network: none}}"), after, VERSION)
        line = next(str(s) for s in steps if s.target == "personal" and s.kind == "pref")
        self.assertEqual(line, "personal: set netvm = (none) (was sys-lintel; before lintel: sys-firewall, "
                               "`qvm-prefs personal netvm sys-firewall` to restore)")
        self.assertEqual(simulate(after, steps).qubes["personal"].netvm, "")

    def test_main_proxy_is_kept_when_unused(self):
        after = simulate(snap(), plan.make(full_machine(), snap(), VERSION))
        steps = plan.make(mparse("qubes: {}"), after, VERSION)
        self.assertFalse([s for s in steps if s.kind == "remove"], steps)
        self.assertIn("sys-lintel: unused (kept: it holds identities)", plan.notes(mparse("qubes: {}"), after))

    def test_unknown_netvm_needs_adopt_force(self):
        s = with_qube(with_qube(snap(), "sys-whonix", "sys-firewall", provides_network=True, qid=61),
                      "anon-whonix", "sys-whonix")
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("qubes: {anon-whonix: {network: proxy}}"), s, VERSION)
        self.assertIn("anon-whonix: its netvm is sys-whonix, which lintel doesn't know", str(e.exception))
        self.assertIn("adopt: force", str(e.exception))
        plan.make(mparse("qubes: {anon-whonix: {network: proxy, adopt: force}}"), s, VERSION)
        # Writing its netvm changes nothing, so nothing to refuse.
        plan.make(mparse("qubes: {anon-whonix: {network: sys-whonix}}"), s, VERSION)

    def test_known_netvms_are_adopted_without_force(self):
        s = with_qube(snap(), "w", "")  # no netvm
        plan.make(mparse("qubes: {w: {network: proxy}, personal: {network: proxy}}"), s, VERSION)  # sys-firewall: the default
        after = simulate(snap(), plan.make(full_machine(), snap(), VERSION))
        after = with_qube(after, "w2", "sys-lintel")  # already behind a lintel proxy
        plan.make(mparse("qubes: {w2: {network: none}}"), after, VERSION)

    def test_show_names_the_adoption(self):
        from lintel import show
        after = simulate(snap(), plan.make(full_machine(), snap(), VERSION))
        v = show.qube_view(after, "personal", {})
        self.assertEqual(v["adopted"], {"from": "sys-firewall", "on": TODAY})
        self.assertIn(f"  adopted        from netvm sys-firewall on {TODAY}", show.format_view(dict(v, pattern="x")))

    def test_disposable_templates_need_disposables_yes(self):
        s = with_qube(snap(), "browser-dvm", "sys-firewall", dvm=True)
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("qubes: {browser-dvm: {network: proxy}}"), s, VERSION)
        self.assertIn("browser-dvm is a disposable template", str(e.exception))
        plan.make(mparse("qubes: {browser-dvm: {network: proxy, disposables: yes}}"), s, VERSION)
        with self.assertRaises(plan.PlanError) as e:
            plan.make(mparse("qubes: {personal: {network: proxy, disposables: yes}}"), s, VERSION)
        self.assertIn("personal is not a disposable template", str(e.exception))


class Chunks(unittest.TestCase):
    def test_roundtrip_and_qubesdb_limit(self):
        m = full_machine()
        big = {"workloads": {f"q{i}": {"internet_allow": [f"h{i}-{j}.example.org" for j in range(40)]}
                             for i in range(40)}}
        for cfg in (derive.proxy_config(m, "sys-lintel", {}, "v"), big):
            f = derive.encode_chunks(cfg)
            self.assertTrue(all(len(v) < 3072 for v in f.values()))
            self.assertEqual(derive.decode_chunks(f), json.loads(json.dumps(cfg)))
        self.assertGreater(len(derive.encode_chunks(big)), 2)

    def test_half_written_is_refused(self):
        big = {"x": [os.urandom(8).hex() for _ in range(2000)]}
        f = derive.encode_chunks(big)
        f[f"{derive.CONFIG_FEATURE}.1"] = f[f"{derive.CONFIG_FEATURE}.1"][::-1]
        with self.assertRaises(Exception):
            derive.decode_chunks(f)


if __name__ == "__main__":
    unittest.main()
