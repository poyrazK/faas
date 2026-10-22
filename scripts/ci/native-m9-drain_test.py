#!/usr/bin/env python3
"""spec: §14.A; adr: 1527 — automatic drain qualification contracts; no faults."""
import contextlib
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("m9_contracts", Path(__file__).with_name("native-m9-gate_test.py"))
contracts = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(contracts)
gate = contracts.gate


def fixtures():
    run, revision, c, bundle, old = contracts.fixtures()
    c["drain"] = {"api_url": "https://control.example.test", "schedd_sha256": "d" * 64}
    pre = old["preflight"]
    pre["source_generation"] = 11
    for peer in pre["peers"].values():
        identity = dict(peer["initial"])
        identity["pid"] += 1000
        identity.pop("vmmd_sha256")
        identity["schedd_sha256"] = "d" * 64
        peer["scheduler"] = {"initial": identity, "paused": None}
    source, dest = pre["peers"]["source"], pre["peers"]["destination"]
    boot = old["fixture-boot"]
    initial = dest["scheduler"]["initial"]
    receipt = {"NodeID": source["node_id"], "NodeName": "source", "SourceGeneration": 12, "ResultGeneration": 13,
               "SourceLifecycle": "draining", "ResultLifecycle": "maintenance", "AuditOutboxID": 99,
               "InitiatedAt": "2026-10-18T00:00:00Z", "CompletedAt": "2026-10-18T00:00:12Z"}
    proofs = {
        "preflight": pre,
        "fixture-boot": boot,
        "drain-pending": {"instance_id": boot["instance_id"], "source_node_id": source["node_id"], "generation": 12,
                          "lifecycle": "draining", "placement_refused": True, "completion_absent": True,
                          "paused_scheduler": {"paused": True, "daemon": initial}, "ram_mb": 512, "cpu_millicores": 500, "probe": boot["probe"]},
        "schedd-restart": {"killed": initial, "restarted": dict(initial, pid=initial["pid"] + 1, invocation_id="e" * 32), "drain_generation": 12},
        "automatic-migration": {"instance_id": boot["instance_id"], "node_id": dest["node_id"], "wake_id": contracts.uid(),
                                "source": old["lost-acknowledgement"]["source_before_ack"], "probe": boot["probe"], "elapsed_ms": 8000},
        "drain-completion": {"receipt": receipt, "source": old["lost-acknowledgement"]["source_after_ack"],
                             "api": {"node_name": "source", "lifecycle": "maintenance", "drain_initiated_at": receipt["InitiatedAt"],
                                     "drain_completed_at": receipt["CompletedAt"]},
                             "source_empty": True, "receipt_unchanged": True, "elapsed_ms": 12000},
        "fixture-cleanup": dict(old["fixture-cleanup"], source_lifecycle="maintenance"),
    }
    return run, revision, c, bundle, json.loads(json.dumps(proofs))


class DrainVerdictTests(unittest.TestCase):
    def verify(self, f, proofs=None, rows=None):
        run, revision, c, bundle, p = f
        if rows is None:
            rows = contracts.events(run, revision, proofs or p, "drain")
        return gate.verify_events(map(json.dumps, rows), run, revision, c, bundle, "drain")

    def test_complete_automatic_drain_and_immutable_completion(self):
        f = fixtures()
        self.assertEqual(self.verify(f), f[4])
        self.assertEqual(gate.configuration(json.dumps(f[2])), f[2])
        f[4]["preflight"]["previous_drain_completed_at"] = "2026-10-17T00:00:00Z"
        f[4]["drain-pending"]["observed_completed_at"] = "2026-10-17T00:00:00Z"
        self.assertEqual(self.verify(f), f[4])

    def test_stale_cycles_early_completion_cold_fallback_and_leaks_fail(self):
        changes = {
            "stale cycle": lambda p: p["drain-pending"].update(generation=13),
            "admission open": lambda p: p["drain-pending"].update(placement_refused=False),
            "premature receipt": lambda p: p["drain-pending"].update(completion_absent=False),
            "new completion timestamp": lambda p: p["drain-pending"].update(observed_completed_at="2026-10-18T00:00:01Z"),
            "capacity lost": lambda p: p["drain-pending"].update(ram_mb=0),
            "wrong controller": lambda p: p["schedd-restart"]["restarted"].update(schedd_sha256="0" * 64),
            "no replacement": lambda p: p["schedd-restart"]["restarted"].update(invocation_id=p["schedd-restart"]["killed"]["invocation_id"]),
            "cold boot": lambda p: p["automatic-migration"]["probe"].update(boot_id=contracts.uid()),
            "cleanup pending": lambda p: p["drain-completion"]["source"].update(Phase="retiring", Proof=""),
            "resources remain": lambda p: p["drain-completion"].update(source_empty=False),
            "wrong result": lambda p: p["drain-completion"]["receipt"].update(ResultLifecycle="active"),
            "audit missing": lambda p: p["drain-completion"]["receipt"].update(AuditOutboxID=0),
            "changed receipt": lambda p: p["drain-completion"].update(receipt_unchanged=False),
            "API stale": lambda p: p["drain-completion"]["api"].update(drain_completed_at="2026-10-18T00:01:00Z"),
            "leak": lambda p: p["fixture-cleanup"]["leaks_after"]["source"].update(passed=False),
            "over budget": lambda p: p["drain-completion"].update(elapsed_ms=999999),
        }
        for name, change in changes.items():
            with self.subTest(name=name):
                f = fixtures()
                p = copy.deepcopy(f[4])
                change(p)
                with self.assertRaises(ValueError):
                    self.verify(f, p)

    def test_skip_missing_phase_and_wrong_scenario_cannot_qualify(self):
        f = fixtures()
        rows = contracts.events(f[0], f[1], f[4], "drain")
        for altered in (rows[:-1], rows[:8] + rows[11:]):
            with self.assertRaises(ValueError):
                self.verify(f, rows=altered)
        skipped = copy.deepcopy(rows)
        skipped[10]["Action"] = "skip"
        with self.assertRaises(ValueError):
            self.verify(f, rows=skipped)
        with self.assertRaises(ValueError):
            gate.verify_events(map(json.dumps, rows), f[0], f[1], f[2], f[3], "migration")

    def test_drain_config_disallows_credentials_plain_http_and_unknown_fields(self):
        for origin in ("http://control.example.test", "https://token@control.example.test", "https://control.example.test/path",
                       "https://control.example.test?private", "https://control.example.test#fragment"):
            c = fixtures()[2]
            c["drain"]["api_url"] = origin
            with self.assertRaises(ValueError):
                gate.configuration(json.dumps(c))
        c = fixtures()[2]
        c["drain"]["token"] = "must-stay-in-environment"
        with self.assertRaises(ValueError):
            gate.configuration(json.dumps(c))

    def test_runner_requires_scheduler_restoration_before_releasing_leases(self):
        for failure in (None, "scheduler-restore", "leak", "skip"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                _, revision, c, _, proofs = fixtures()
                d = Path(directory)
                path = d / "input.json"
                path.write_text(json.dumps(c))
                files = {n: (gate.ROOT / "deploy/scripts" / n).read_text() for n in ("leakcheck.sh", "leakcheck_native_loops.py")}
                bundle = {n: gate.hashlib.sha256(body.encode()).hexdigest() for n, body in files.items()}
                leak = {"passed": True, "bundle_sha256": bundle}
                for name in ("source", "destination"):
                    proofs["preflight"]["peers"][name]["leaks_before"] = leak
                    proofs["fixture-cleanup"]["leaks_after"][name] = leak
                def run(_c, run_id, _revision, _path, output, scenario):
                    self.assertEqual(scenario, "drain")
                    rows = contracts.events(run_id, revision, proofs, "drain")
                    if failure == "skip":
                        rows[10]["Action"] = "skip"
                    (output / "go-test.jsonl").write_text("\n".join(map(json.dumps, rows)) + "\n")
                    return 0
                calls = []
                def peer(p, _run, action, _files):
                    calls.append((p["name"], action))
                    self.assertEqual(p["schedd_sha256"], c["drain"]["schedd_sha256"])
                    if action == "restore":
                        return {"restored": True, "scheduler": {"restored": failure != "scheduler-restore",
                                "daemon": proofs["preflight"]["peers"][p["name"]]["scheduler"]["initial"]}}
                    return dict(leak, released=True, passed=failure != "leak")
                with patch.dict(gate.os.environ, {"FAAS_M9_CONFIG": str(path), "FAAS_M9_OPERATOR_TOKEN": "private-token"}), \
                     patch.object(gate.tempfile, "mkdtemp", return_value=directory), \
                     patch.object(gate, "command", side_effect=lambda args, **_kw: "" if "status" in args else revision), \
                     patch.object(gate, "preflight", return_value={}), patch.object(gate, "run_test", side_effect=run), \
                     patch.object(gate, "peer_action", side_effect=peer), patch.object(gate.signal, "signal"), \
                     contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(gate.main("drain"), 0 if failure is None else 1)
                report = json.loads((d / "report.json").read_text())
                self.assertEqual(report["scenario"], "drain")
                self.assertEqual(report["native_qualified"], failure is None)
                self.assertNotIn("private-token", json.dumps(report))
                if failure is not None:
                    self.assertFalse(any(action == "release" for _, action in calls))


if __name__ == "__main__":
    unittest.main()
