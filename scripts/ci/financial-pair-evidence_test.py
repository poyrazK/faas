#!/usr/bin/env python3
"""Portable rejection contracts; synthetic hosts never count as acceptance."""
import copy
import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("pair", Path(__file__).with_name("financial-pair-evidence.py"))
pair = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pair)


class PairEvidenceTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.records = self.root / "records"
        self.records.mkdir()
        self.sha, self.run_id = "a" * 40, "contract"
        source = self.root / pair.PAIR_SOURCE
        source.parent.mkdir(parents=True)
        source.write_text('func TestMetalFinancialPairSharedBudget(t *testing.T) { t.Run("contention", fn) }\nfunc TestMetalFinancialPairDatabasePartitions(t *testing.T) { t.Run("node_a", fn); t.Run("node_b", fn) }\n')
        source.with_name("financial_compute_egress_metal_test.go").write_text("func TestMetalFinancialEgressPacketCredits(t *testing.T) {}\nfunc TestMetalFinancialEgressOffloadCredits(t *testing.T) {}\n")
        component, _ = pair.source_tests(self.root)
        tests, _ = pair.source_tests(self.root, pair=True)
        output = []
        for name, phases in {**component, **tests}.items():
            output.extend(f"    --- PASS: {name}/{phase} (0.1s)" for phase in phases)
            output.append(f"--- PASS: {name} (1.0s)")
        output.append("PASS")
        assets = {name: "c" * 64 for name in pair.ASSETS}
        pair.write_json(self.records / "fixture.json", {"assets": assets, "archive_sha256": "f" * 64})
        for index, node in enumerate(("a", "b")):
            directory = self.records / ("host-" + node)
            directory.mkdir()
            (directory / "leakcheck.log").write_text("synthetic successful cleanup\n")
            host = {"source_sha": self.sha, "run_id": self.run_id, "root": True, "kvm": True, "architecture": "x86_64", "machine_id_sha256": str(index + 1) * 64, "boot_id_sha256": str(index + 3) * 64, "assets": assets, "tools": {name: "d" * 64 for name in ("firecracker", "jailer")}}
            pair.write_json(directory / "ready.json", host)
            pair.write_json(directory / "cleanup.json", {**host, "runner_exit": 0, "leakcheck_exit": 0, "post_repair_exit": 0, "restore_exit": 0, "leakcheck_sha256": pair.digest(directory / "leakcheck.log")})
            owner = {"node": index, "instance": f"fixture-{index}", "host": {**host, "architecture": "amd64"}}
            output.append("test.go:1: financial-pair-owner:" + json.dumps(owner))
        (self.records / "native.log").write_text("\n".join(output) + "\n")
        (self.records / "peer-network.log").write_text("\n".join(f"--- PASS: {name} (0.1s)" for name in component) + "\nPASS\n")
        pair.write_json(self.records / "commands.json", {name: 0 for name in ("single_host", "native", "peer_network", "host_a", "host_b")})
        # Reuse the full-system verdict's synthetic fixture, including raw
        # receipts/wire events. Pair acceptance must validate that boundary too.
        fixture_spec = importlib.util.spec_from_file_location("full_fixture", Path(__file__).with_name("financial-system-verdict_test.py"))
        fixture = importlib.util.module_from_spec(fixture_spec)
        fixture_spec.loader.exec_module(fixture)
        case = fixture.VerdictTest()
        case.setUp()
        self.addCleanup(case.doCleanups)
        old_root = case.args.root
        for source_name in (*fixture.verdict.SOURCES.values(), *fixture.verdict.REMOTE_SUPPORT):
            path = self.root / source_name
            path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(old_root / source_name, path)
        collected = self.records / "single-host"
        collected.mkdir()
        for name in ("native_log", "provider_log"):
            old = getattr(case.args, name)
            path = collected / old.name
            shutil.copyfile(old, path)
            setattr(case.args, name, path)
        shutil.copytree(old_root / "financial-system-records", collected / "financial-system-records")
        shutil.copytree(old_root / "financial-system-records", self.records / "reconciliation")
        case.args.root, case.args.source_sha, case.args.run_id = self.root, self.sha, self.run_id
        report = case.report()
        self.assertTrue(report["qualified"], report["failures"])
        pair.write_json(self.records / "single-host.json", report)
        pair.write_json(collected / ("financial-system-verdict-" + self.run_id + ".json"), report)

    def report(self):
        return pair.build_report(self.root, self.records, self.sha, self.run_id)

    def change_json(self, relative, mutate):
        path = self.records / relative
        original = json.loads(path.read_text())
        changed = copy.deepcopy(original)
        mutate(changed)
        pair.write_json(path, changed)
        self.assertFalse(self.report()["qualified"])
        pair.write_json(path, original)

    def test_complete_pair_is_scoped_and_never_promotes_without_matrix(self):
        report = self.report()
        self.assertTrue(report["qualified"], report["failures"])
        self.assertFalse(report["promotion_ready"])
        self.assertTrue(report["remaining_release_gates"])
        self.assertEqual(len(report["hosts"]), 2)

    def test_same_host_or_boot_wrong_hardware_and_artifacts_fail(self):
        a = json.loads((self.records / "host-a/ready.json").read_text())
        for field, value in (("machine_id_sha256", a["machine_id_sha256"]), ("boot_id_sha256", a["boot_id_sha256"]), ("kvm", False), ("root", False), ("architecture", "aarch64"), ("source_sha", "b" * 40), ("run_id", "stale"), ("assets", {}), ("tools", {})):
            with self.subTest(field=field):
                self.change_json("host-b/ready.json", lambda data: data.update({field: value}))

    def test_every_cleanup_and_command_result_is_mandatory(self):
        for field in ("runner_exit", "leakcheck_exit", "post_repair_exit", "restore_exit"):
            with self.subTest(field=field):
                self.change_json("host-b/cleanup.json", lambda data: data.update({field: 1}))
        for field in ("single_host", "native", "peer_network", "host_a", "host_b"):
            with self.subTest(command=field):
                self.change_json("commands.json", lambda data: data.update({field: 1}))
        (self.records / "host-b/leakcheck.log").write_text("changed cleanup\n")
        self.assertFalse(self.report()["qualified"])

    def test_missing_repeated_skipped_failed_and_early_parent_fail(self):
        path = self.records / "native.log"
        original = path.read_text()
        for bad in ("PASS\n", original + "--- SKIP: TestOther (0s)\n", original + "    --- FAIL: TestOther/phase (0s)\n", original + "--- PASS: TestMetalFinancialPairSharedBudget (0.1s)\n", original.replace("    --- PASS: TestMetalFinancialPairDatabasePartitions/node_b (0.1s)\n", "")):
            with self.subTest(log=bad[:60]):
                path.write_text(bad)
                self.assertFalse(self.report()["qualified"])
        path.write_text(original)
        self.assertTrue(self.report()["qualified"])

    def test_missing_owner_wrong_owner_and_new_source_scenario_fail(self):
        path = self.records / "native.log"
        original = path.read_text()
        path.write_text("\n".join(line for line in original.splitlines() if '"node": 1' not in line) + "\n")
        self.assertFalse(self.report()["qualified"])
        path.write_text(original.replace('"node": 1', '"node": 0'))
        self.assertFalse(self.report()["qualified"])
        path.write_text(original)
        source = self.root / pair.PAIR_SOURCE
        source.write_text(source.read_text() + "func TestMetalFinancialPairNewScenario(t *testing.T) {}\n")
        self.assertFalse(self.report()["qualified"])

    def test_stale_billing_missing_vectors_and_fixture_manifest_fail(self):
        for field, value in (("qualified", False), ("source_sha", "b" * 40), ("run_id", "stale"), ("reconciliation", {})):
            self.change_json("single-host.json", lambda data: data.update({field: value}))
        self.change_json("fixture.json", lambda data: data.update(assets={}))
        (self.records / "reconciliation/reconciliation-unavailable.json").unlink()
        self.assertFalse(self.report()["qualified"])

    def test_missing_or_malformed_evidence_fails_closed(self):
        for name in ("host-b/ready.json", "host-b/cleanup.json", "native.log", "peer-network.log", "commands.json", "single-host.json"):
            with self.subTest(file=name):
                path = self.records / name
                original = path.read_text()
                path.write_text("invalid json\n")
                self.assertFalse(self.report()["qualified"])
                path.unlink()
                self.assertFalse(self.report()["qualified"])
                path.write_text(original)


if __name__ == "__main__":
    unittest.main()
