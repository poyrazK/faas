#!/usr/bin/env python3
import argparse
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("verdict", Path(__file__).with_name("financial-system-verdict.py"))
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


class VerdictTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        self.args = argparse.Namespace(root=root, source_sha="a" * 40, run_id="fixture", native_log=root / "native.log", provider_log=root / "provider.log", native_exit=0, provider_exit=0, leakcheck_exit=0, restore_exit=0, runner_exit=0, binary_root=root / "bin", guest_init=root / "guest", kernel=root / "kernel")
        self.host = {"root": True, "kvm": True, "architecture": "x86_64"}
        for lane, source in verdict.SOURCES.items():
            path = root / source
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('func TestFinancialSystemFixture(t *testing.T) { t.Run("outage", fn) }\n')
            log = getattr(self.args, lane + "_log")
            log.write_text('    --- PASS: TestFinancialSystemFixture/outage (0.1s)\n--- PASS: TestFinancialSystemFixture (1.0s)\nPASS\nok  github.com/onebox-faas/faas/fixture 1.1s\n')
        for source in verdict.REMOTE_SUPPORT:
            path = root / source
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("synthetic supporting source; never native execution\n")
        (root / verdict.REMOTE_SOURCE).write_text('\n'.join(f't.Run("{name}", func(t *testing.T) {{ check(t, "{version}", "{persistent}") }})' for name, version, persistent in (("legacy_v1", "v1", "0"), ("per_request_v2", "v2", "0"), ("persistent_v2", "v2", "1"))))
        with self.args.native_log.open("a") as log:
            for name, version, persistent in (("legacy_v1", "v1", "0"), ("per_request_v2", "v2", "0"), ("persistent_v2", "v2", "1")):
                log.write(f'    --- PASS: TestFinancialSystemFixture/remote_http_variants/{name} (0.1s)\nfinancial-remote-http: version={version} persistent={persistent} payload=10000 interface_delta=11000 cumulative=33000 funded=65536\n')
            log.write('financial-remote-exhaustion: payload=10000 interface_delta=11000 cumulative=44000 funded=65536 private_payload=10000 stable_after=44000 private_counter=44000\nfinancial-remote-restart: payload=10000 interface_delta=11000 cumulative=11000 funded=65536 retired_payload=0\n')
        for path in [self.args.binary_root / name for name in verdict.BINARIES] + [self.args.guest_init, self.args.kernel]:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"synthetic artifact; never native execution")
        provider_source = root / verdict.SOURCES["provider"]
        provider_source.write_text(provider_source.read_text() + '\nfor _, scenario := range []string{"unavailable", "lost_reply"} {}\n')
        records = root / "financial-system-records"
        records.mkdir()
        for scenario in ("unavailable", "lost_reply"):
            proof = {"scenario": scenario, "expected": {"compute": 3686400, "egress": 1073741824}, "receipts": {"compute": 3686400, "egress": 1073741824}, "invoice_millicents": 3000, "committed_millicents": 3000, "limit_millicents": 4000, "measured_mb_seconds": 3686400, "funded_mb_seconds": 3686400, "measured_egress_bytes": 1073741824, "funded_egress_bytes": 1073741824, "provider": {"conflicts": [], "events": {"compute": {"external_id": "compute", "name": "faas_ram_usage", "metadata": {"mb_seconds": 3686400}}, "egress": {"external_id": "egress", "name": "faas_egress_usage", "metadata": {"egress_bytes": 1073741824}}}}}
            (records / ("reconciliation-" + scenario + ".json")).write_text(json.dumps(proof))

    def report(self):
        return verdict.build_report(self.args, self.host)

    def test_complete_contract_and_hashes(self):
        report = self.report()
        self.assertTrue(report["qualified"], report["failures"])
        self.assertEqual(report["lanes"]["native"]["log_sha256"], verdict.digest(self.args.native_log))
        self.assertTrue(report["remaining_release_gates"])

    def test_missing_failed_skipped_and_empty_evidence(self):
        original = self.args.native_log.read_text()
        for bad in ["PASS\n", original.replace("--- PASS: TestFinancialSystemFixture (", "--- SKIP: TestFinancialSystemFixture ("), original + "    --- SKIP: TestFinancialSystemFixture/missing (0s)\n", original + "    --- FAIL: TestOther (0s)\n", original.replace("    --- PASS: TestFinancialSystemFixture/outage (0.1s)\n", ""), original.replace("ok  ", "FAIL ")]:
            with self.subTest(log=bad):
                self.args.native_log.write_text(bad)
                self.assertFalse(self.report()["qualified"])
        self.args.native_log.unlink()
        self.assertFalse(self.report()["qualified"])

    def test_every_command_status_is_required(self):
        for name in ["native_exit", "provider_exit", "leakcheck_exit", "restore_exit", "runner_exit"]:
            with self.subTest(check=name):
                setattr(self.args, name, 1)
                self.assertFalse(self.report()["qualified"])
                setattr(self.args, name, 0)

    def test_remote_payload_billing_and_restart_proofs_are_required(self):
        original = self.args.native_log.read_text()
        for before, after in (("payload=10000 interface_delta=11000", "payload=0 interface_delta=11000"), ("interface_delta=11000", "interface_delta=9999"), ("interface_delta=11000", "interface_delta=20000"), ("funded=65536", "funded=1"), ("private_counter=44000", "private_counter=54000"), ("retired_payload=0", "retired_payload=1"), ("persistent_v2 (0.1s)", "missing_v2 (0.1s)")):
            with self.subTest(before=before):
                self.args.native_log.write_text(original.replace(before, after))
                self.assertFalse(self.report()["qualified"])
        self.args.native_log.write_text(original)
        (self.args.binary_root / "vmmd-stream-bridge").unlink()
        self.assertFalse(self.report()["qualified"])

    def test_provider_raw_receipts_quantities_and_money_are_required(self):
        path = self.args.root / "financial-system-records/reconciliation-lost_reply.json"
        original = json.loads(path.read_text())
        for field, value in (("receipts", {}), ("funded_egress_bytes", 1), ("committed_millicents", 1), ("invoice_millicents", 1), ("invoice_millicents", 3000.0)):
            with self.subTest(field=field):
                path.write_text(json.dumps({**original, field: value}))
                self.assertFalse(self.report()["qualified"])
        original["provider"]["events"]["egress"]["metadata"]["egress_bytes"] += 1
        path.write_text(json.dumps(original))
        self.assertFalse(self.report()["qualified"])
        path.unlink()
        self.assertFalse(self.report()["qualified"])

    def test_new_source_scenario_requires_new_execution(self):
        path = self.args.root / verdict.SOURCES["native"]
        path.write_text(path.read_text() + "func TestFinancialSystemNewCase(t *testing.T) {}\n")
        self.assertFalse(self.report()["qualified"])
        path.unlink()
        self.assertFalse(self.report()["qualified"])

    def test_no_native_host_no_artifact_or_invalid_source(self):
        for field, value in [("root", False), ("kvm", False), ("architecture", "aarch64")]:
            with self.subTest(host=field):
                host = dict(self.host)
                self.host[field] = value
                self.assertFalse(self.report()["qualified"])
                self.host = host
        self.args.source_sha = "main"
        self.assertFalse(self.report()["qualified"])
        self.args.source_sha = "a" * 40
        self.args.kernel.unlink()
        self.assertFalse(self.report()["qualified"])


if __name__ == "__main__":
    unittest.main()
