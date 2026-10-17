#!/usr/bin/env python3
"""spec: §14.A; adr: 1530 — portable verdict and orchestration contracts."""
import copy
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import uuid

SPEC = importlib.util.spec_from_file_location("gate", Path(__file__).with_name("native-m9-gate.py"))
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


def uid():
    return str(uuid.uuid4())


def fixtures():
    run, revision = uid(), "a" * 40
    bundle = {"leakcheck.sh": "a" * 64, "leakcheck_native_loops.py": "b" * 64}
    leak = {"passed": True, "bundle_sha256": bundle}
    c = {"version": 1, "app_id": uid(), "deployment_id": uid(), "fixture_id": uid(), "timeout_seconds": 180}
    peers = {}
    for name in ("source", "destination"):
        c[name] = {"name": name, "ssh": "faas@" + name, "socket": "/run/faas/vmmd.sock", "vmmd_sha256": "c" * 64}
        peers[name] = {"node_id": uid(), "boot_id": uid(), "runtime_incarnation": uid(), "firecracker_version": "1.14.0",
                       "initial": {"pid": len(peers) + 100, "invocation_id": uuid.uuid4().hex,
                                   "vmmd_sha256": "c" * 64, "start_ticks": "123"}, "leaks_before": leak}
    instance, wake = uid(), uid()
    probe = {"fixture_id": c["fixture_id"], "boot_id": uid(), "operation_id": uid(), "sequence": 1, "unique_operations": 1}
    before = {"InstanceID": instance, "SourceNodeID": peers["source"]["node_id"], "SourceWakeID": wake,
              "LeaseToken": "lease", "NativeGeneration": uid(), "PhysicalBinding": "d" * 64,
              "RAMMB": 512, "CPUMillicores": 500, "VCPU": 1, "Phase": "retiring", "Proof": ""}
    after = dict(before, Phase="retired", Proof="generation-scoped-cleanup")
    proofs = {
        "preflight": {"app_id": c["app_id"], "deployment_id": c["deployment_id"], "peers": peers},
        "fixture-boot": {"instance_id": instance, "wake_id": wake, "node_id": peers["source"]["node_id"], "probe": probe},
        "lost-acknowledgement": {"instance_id": instance, "source_before_ack": before, "source_after_ack": after,
                                 "destination_node_id": peers["destination"]["node_id"], "destination_wake_id": uid(),
                                 "response_discarded": True, "retained_capacity": {"ram_mb": 512, "cpu_millicores": 500}, "probe": probe},
        "vmmd-restart": {"killed": peers["source"]["initial"], "restarted": dict(peers["source"]["initial"], invocation_id=uuid.uuid4().hex),
                         "retained_source": after, "runtime_incarnation": uid()},
        "receipt-replay": {"source": after, "probe": probe},
        "replacement-protection": {"instance_id": instance, "wake_id": uid(), "native_generation": uid(),
                                   "old_source_generation": before["NativeGeneration"],
                                   "probe": dict(probe, operation_id=uid(), sequence=2, unique_operations=2)},
        "fixture-cleanup": {"instance_id": instance, "leaks_after": {"source": leak, "destination": leak}},
    }
    return run, revision, c, bundle, copy.deepcopy(proofs)


def events(run, revision, proofs, scenario="migration"):
    name, phases = (gate.DRAIN_TEST, gate.DRAIN_PHASES) if scenario == "drain" else (gate.TEST, gate.PHASES)
    rows = [{"Action": "start"}, {"Action": "run", "Test": name}]
    for phase in phases:
        test = name + "/" + phase
        proof = {"version": 1, "run_id": run, "revision": revision, "phase": phase, "details": proofs[phase]}
        rows.extend([{"Action": "run", "Test": test}, {"Action": "output", "Test": test,
                      "Output": "    native_test.go:42: M9_EVIDENCE " + json.dumps(proof) + "\n"},
                     {"Action": "pass", "Test": test}])
    rows.extend([{"Action": "pass", "Test": name}, {"Action": "pass"}])
    return [dict(row, Package=gate.PACKAGE) for row in rows]


class VerdictTests(unittest.TestCase):
    def verify(self, rows, f):
        run, revision, c, bundle, _ = f
        return gate.verify_events([json.dumps(row) for row in rows], run, revision, c, bundle)

    def test_complete_real_phase_stream(self):
        f = fixtures()
        self.assertEqual(self.verify(events(f[0], f[1], f[4]), f), f[4])

    def test_skipped_missing_duplicate_foreign_and_truncated_streams_fail(self):
        f = fixtures()
        rows = events(f[0], f[1], f[4])
        cases = {"missing root": rows[:1] + rows[2:], "truncated": rows[:-1],
                 "missing proof": rows[:3] + rows[4:], "duplicate phase": rows[:5] + rows[2:5] + rows[5:]}
        for name, field, value in (("skip", "Action", "skip"), ("fail", "Action", "fail"), ("foreign package", "Package", "other")):
            changed = copy.deepcopy(rows)
            changed[4][field] = value
            cases[name] = changed
        for name, changed in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.verify(changed, f)
        with self.assertRaises(ValueError):
            gate.verify_events(["{partial"], *f[:4])
        for run, revision in ((uid(), f[1]), (f[0], "b" * 40)):
            with self.assertRaises(ValueError):
                gate.verify_events(map(json.dumps, rows), run, revision, f[2], f[3])

    def test_false_success_evidence_cannot_qualify(self):
        changes = {
            "capacity released": lambda p: p["lost-acknowledgement"]["retained_capacity"].update(ram_mb=0),
            "no lost reply": lambda p: p["lost-acknowledgement"].update(response_discarded=False),
            "changed receipt": lambda p: p["receipt-replay"]["source"].update(Proof="other"),
            "cold fallback": lambda p: p["lost-acknowledgement"]["probe"].update(boot_id=uid()),
            "duplicate operation": lambda p: p["replacement-protection"]["probe"].update(sequence=3),
            "same generation": lambda p: p["replacement-protection"].update(native_generation=p["lost-acknowledgement"]["source_after_ack"]["NativeGeneration"]),
            "peer leak": lambda p: p["fixture-cleanup"]["leaks_after"]["destination"].update(passed=False),
            "wrong artifact": lambda p: p["preflight"]["peers"]["source"]["initial"].update(vmmd_sha256="0" * 64),
        }
        for name, change in changes.items():
            with self.subTest(name=name):
                f = fixtures()
                # Break shared dictionaries before mutating one observation.
                proof = json.loads(json.dumps(f[4]))
                change(proof)
                with self.assertRaises(ValueError):
                    self.verify(events(f[0], f[1], proof), f)

    def test_config_rejects_remote_shell_and_ambiguous_input(self):
        f = fixtures()
        self.assertEqual(gate.configuration(json.dumps(f[2])), f[2])
        for field, value in (("ssh", "-oProxyCommand=id"), ("ssh", "host;id"), ("socket", "/run/faas/../other.sock")):
            c = copy.deepcopy(f[2])
            c["source"][field] = value
            with self.assertRaises(ValueError):
                gate.configuration(json.dumps(c))
        with self.assertRaises(ValueError):
            gate.configuration('{"version":1,"version":2}')

    def test_runner_restores_both_peers_and_retains_leases_after_failure(self):
        _, _, c, _, _ = fixtures()
        with tempfile.TemporaryDirectory() as directory:
            d = Path(directory)
            config = d / "input.json"
            config.write_text(json.dumps(c))
            calls = []
            def peer(peer, _run, action, _files):
                calls.append((peer["name"], action))
                if action == "restore":
                    return {"restored": True}
                return {"passed": False}
            with patch.dict(gate.os.environ, {"FAAS_M9_CONFIG": str(config)}), \
                 patch.object(gate.tempfile, "mkdtemp", return_value=directory), \
                 patch.object(gate, "command", return_value="a" * 40), \
                 patch.object(gate, "preflight", return_value={}), \
                 patch.object(gate, "run_test", return_value=1), \
                 patch.object(gate, "peer_action", side_effect=peer), \
                 patch.object(gate.signal, "signal"), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(gate.main(), 1)
            report = json.loads((d / "report.json").read_text())
            self.assertFalse(report["native_qualified"])
            self.assertIn(("source", "restore"), calls)
            self.assertIn(("destination", "restore"), calls)
            self.assertFalse(any(action == "release" for _, action in calls))

    def test_zero_exit_needs_complete_evidence_and_independent_peer_verdicts(self):
        for failure in (None, "skipped", "peer-leak", "release"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                f = fixtures()
                _, revision, c, _, proofs = f
                d = Path(directory)
                path = d / "input.json"
                path.write_text(json.dumps(c))
                files = {name: (gate.ROOT / "deploy/scripts" / name).read_text() for name in f[3]}
                bundle = {name: gate.hashlib.sha256(body.encode()).hexdigest() for name, body in files.items()}
                leak = {"passed": True, "bundle_sha256": bundle}
                for name in ("source", "destination"):
                    proofs["preflight"]["peers"][name]["leaks_before"] = leak
                    proofs["fixture-cleanup"]["leaks_after"][name] = leak
                def run(_config, run_id, _revision, _path, output):
                    rows = events(run_id, revision, proofs)
                    if failure == "skipped":
                        rows[4]["Action"] = "skip"
                    (output / "go-test.jsonl").write_text("\n".join(map(json.dumps, rows)) + "\n")
                    return 0
                calls = []
                def peer(p, _run, action, _files):
                    calls.append((p["name"], action))
                    if failure == "release" and action == "release":
                        raise ConnectionError("release response lost")
                    if action == "restore":
                        return {"restored": True}
                    return dict(leak, passed=not (failure == "peer-leak" and p["name"] == "source"), released=True)
                with patch.dict(gate.os.environ, {"FAAS_M9_CONFIG": str(path)}), \
                     patch.object(gate.tempfile, "mkdtemp", return_value=directory), \
                     patch.object(gate, "command", side_effect=lambda args, **_kw: "" if "status" in args else revision), \
                     patch.object(gate, "preflight", return_value={}), patch.object(gate, "run_test", side_effect=run), \
                     patch.object(gate, "peer_action", side_effect=peer), patch.object(gate.signal, "signal"), \
                     contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(gate.main(), 0 if failure is None else 1)
                report = json.loads((d / "report.json").read_text())
                self.assertEqual(report["native_qualified"], failure is None)
                self.assertIn(("source", "restore"), calls)
                self.assertIn(("destination", "restore"), calls)
                if failure in ("skipped", "peer-leak"):
                    self.assertFalse(any(action == "release" for _, action in calls))

    def test_preflight_failure_never_starts_drill_or_contacts_peers(self):
        f = fixtures()
        with tempfile.TemporaryDirectory() as directory:
            d = Path(directory)
            path = d / "input.json"
            path.write_text(json.dumps(f[2]))
            with patch.dict(gate.os.environ, {"FAAS_M9_CONFIG": str(path)}), \
                 patch.object(gate.tempfile, "mkdtemp", return_value=directory), \
                 patch.object(gate, "command", return_value=f[1]), \
                 patch.object(gate, "preflight", side_effect=ValueError("missing native marker")), \
                 patch.object(gate, "run_test") as run, patch.object(gate, "peer_action") as peer, \
                 patch.object(gate.signal, "signal"), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(gate.main(), 1)
                run.assert_not_called()
                peer.assert_not_called()
            self.assertFalse(json.loads((d / "report.json").read_text())["native_qualified"])

    def test_artifact_must_match_exact_clean_checkout_and_peer_hash(self):
        _, revision, c, _, _ = fixtures()
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "vmmd"
            binary.write_bytes(b"reviewed executable")
            digest = gate.hashlib.sha256(binary.read_bytes()).hexdigest()
            for p in (c["source"], c["destination"]):
                p["vmmd_sha256"] = digest
            marker = SimpleNamespace(st_mode=0o100600, st_uid=0)
            build = "\tbuild\tvcs.revision=" + revision + "\n\tbuild\tvcs.modified=false"
            for case in ("valid", "wrong revision", "revision suffix", "dirty artifact", "dirty checkout", "wrong hash"):
                with self.subTest(case=case):
                    metadata = build
                    config = copy.deepcopy(c)
                    if case == "wrong revision":
                        metadata = metadata.replace(revision, "b" * 40)
                    if case == "revision suffix":
                        metadata = metadata.replace(revision, revision + "bad")
                    if case == "dirty artifact":
                        metadata = metadata.replace("modified=false", "modified=true")
                    if case == "wrong hash":
                        config["source"]["vmmd_sha256"] = "0" * 64
                    def command(args, **_kwargs):
                        if "status" in args:
                            return " M tracked.go" if case == "dirty checkout" else ""
                        if "GOVERSION" in args:
                            return "go1.26.9"
                        return metadata
                    with patch.dict(gate.os.environ, {"FAAS_M9_VMMD_BINARY": str(binary), "FAAS_M9_CONFIRM": "native-x86", "DATABASE_URL": "private-dsn"}), \
                         patch.object(gate.os, "geteuid", return_value=0), patch.object(gate.platform, "system", return_value="Linux"), \
                         patch.object(gate.platform, "machine", return_value="x86_64"), patch.object(gate.Path, "lstat", return_value=marker), \
                         patch.object(gate, "command", side_effect=command):
                        if case == "valid":
                            self.assertEqual(gate.preflight(config, revision)["vmmd_sha256"], digest)
                        else:
                            with self.assertRaises(ValueError):
                                gate.preflight(config, revision)


if __name__ == "__main__":
    unittest.main()
