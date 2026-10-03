#!/usr/bin/env python3
"""Exercise host failures and evidence classification without KVM or PostgreSQL."""

import argparse
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from unittest.mock import MagicMock


SCRIPT = Path(__file__).with_name("native-acceptance-preflight.py")
SPEC = importlib.util.spec_from_file_location("preflight", SCRIPT)
preflight = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(preflight)


class FixtureHost:
    def __init__(self, root):
        self.root = root
        self.calls = []
        self.postgres = (0, "t")
        self.virtualization = (0, "google")
        self.free_bytes = 32 << 30

    def path(self, name):
        return self.root / str(name).lstrip("/")

    def put(self, name, content):
        path = self.path(name)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        return path

    def tool(self, name):
        return True

    def identity(self):
        return 0, "Linux", "x86_64", 2

    def available_bytes(self, path):
        return self.free_bytes

    def run(self, argv, env=None):
        self.calls.append((argv, env))
        if argv[-1] == "postgres-probe":
            return self.postgres
        if argv[0] == "go":
            return 0, "go version go1.25.13 linux/amd64"
        if argv[0] == "firecracker":
            return 0, "Firecracker v1.7.0"
        if argv[0] == "systemd-detect-virt":
            return self.virtualization
        if argv[0] == "file":
            return 0, "ELF statically linked"
        return 0, ""


class PreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.host = FixtureHost(self.root)
        self.args = argparse.Namespace(repo_root="/repo", source_sha="1" * 40,
            go="go", kernel="/srv/fc/base/vmlinux", fc_version="1.7.0",
            mode="e2e", lane="containers", runtime_base_key=[])
        for mode in ("e2e", "metal"):
            self.host.put(f"/repo/.faas-{mode}-source-sha", self.args.source_sha)
        for name, content in {
            "/repo/go.mod": "module example\ngo 1.25.13\n",
            "/dev/kvm": "fixture",
            "/etc/faas/builder-acceptance-host": "designated",
            "/sys/fs/cgroup/cgroup.controllers": "cpu memory pids",
            "/sys/fs/cgroup/cgroup.subtree_control": "cpu memory",
            "/proc/sys/net/ipv4/ip_forward": "1",
            "/proc/meminfo": "MemTotal: 16000000 kB\n",
            "/srv/fc/base/vmlinux": "pinned kernel",
            "/srv/fc/kernel/1.7.0": "pinned kernel",
        }.items():
            self.host.put(name, content)
        self.base = self.host.put("/srv/fc/base/runner-builder-amd64.ext4", "base bytes")
        self.scan = self.host.put("/srv/fc/scans/runner-builder-amd64.ext4.scan.json", json.dumps({
            "source": str(self.base), "image": "ghcr.io/example/builder@sha256:" + "a" * 64,
            "findings": {"CRITICAL": 2}, "fix_available_findings": {"CRITICAL": 0},
            "scanned_at": "2026-10-01T00:00:00Z"}))
        self.digest = self.host.put("/srv/fc/base/runner-builder-amd64.ext4.digest",
            "source-ref=ghcr.io/example/builder@sha256:" + "a" * 64 + "\nlayout-v3\n")
        self.host.path("/srv/fc/snap").mkdir()
        self.host.path("/var/run/faas/stream").mkdir(parents=True)
        self.host.put("/repo/pkg/builderd/drive.go",
            (SCRIPT.parents[2] / "pkg/builderd/drive.go").read_text())
        self.env = patch.dict(os.environ, {"FAAS_STORAGE_BACKEND": "local",
            "FAAS_STORAGE_ROOT": "/srv/fc", "FAAS_E2E_DATABASE_URL": "postgres://faas:secret@localhost/test"})
        self.env.start()
        self.addCleanup(self.env.stop)

    def collect(self):
        # A regular fixture stands in for /dev/kvm; no real device is needed.
        with patch.object(preflight.stat, "S_ISCHR", return_value=True):
            return preflight.collect(self.args, self.host)

    def check(self, report, name):
        return next(item for item in report["checks"] if item["name"] == name)

    def test_ready_reports_nested_resources_and_artifact_digests(self):
        report = self.collect()
        self.assertEqual("ready", report["preflight"])
        self.assertEqual(2, report["host"]["cpu_count"])
        self.assertEqual(16000000, report["host"]["memory_kib"])
        self.assertEqual("google", report["host"]["virtualization"])
        self.assertFalse(report["host"]["native_host"])
        self.assertFalse(report["host"]["performance_qualified"])
        self.assertEqual(preflight.sha256(self.base), report["artifacts"]["base/runner-builder-amd64.ext4"]["sha256"])
        self.assertEqual("not_run", report["acceptance"]["status"])

    def test_compatibility_kernel_cannot_hide_missing_canonical_key(self):
        self.host.path("/srv/fc/kernel/1.7.0").unlink()
        report = self.collect()
        self.assertEqual("passed", self.check(report, "test_kernel")["status"])
        self.assertEqual("failed", self.check(report, "canonical_kernel")["status"])
        self.assertIn("/srv/fc/kernel/1.7.0", self.check(report, "canonical_kernel")["repair"])
        self.assertEqual("blocked", report["preflight"])

    def test_wrong_kernel_at_canonical_key_blocks(self):
        self.host.put("/srv/fc/kernel/1.7.0", "wrong kernel")
        self.assertEqual("failed", self.check(self.collect(), "canonical_kernel")["status"])

    def test_snapshot_root_must_be_an_existing_directory_without_mutation(self):
        path = self.host.path("/srv/fc/snap")
        path.rmdir()
        report = self.collect()
        self.assertEqual("blocked", report["preflight"])
        self.assertEqual("failed", self.check(report, "snapshot_directory")["status"])
        self.assertFalse(path.exists())
        path.write_text("not a directory")
        self.assertEqual("failed", self.check(self.collect(), "snapshot_directory")["status"])
        self.assertEqual("not a directory", path.read_text())

    def test_builder_capacity_uses_owner_budget_and_available_blocks(self):
        required = preflight.builder_drive_budget(self.host.path("/repo/pkg/builderd/drive.go").read_text())
        for available in (required - 1, required, required + 1):
            with self.subTest(available=available):
                self.host.free_bytes = available
                result = self.check(self.collect(), "builder_drive_capacity")
                self.assertEqual("passed" if available >= required else "failed", result["status"])
                self.assertEqual(required, result["detail"]["required_bytes"])

    def test_stream_bridge_parent_must_exist_without_mutation(self):
        path = self.host.path("/var/run/faas/stream")
        path.rmdir()
        report = self.collect()
        self.assertEqual("blocked", report["preflight"])
        result = self.check(report, "stream_bridge_directory")
        self.assertEqual("failed", result["status"])
        self.assertIn("mode 0770", result["repair"])
        self.assertFalse(path.exists())
        path.write_text("not a directory")
        self.assertEqual("failed", self.check(self.collect(), "stream_bridge_directory")["status"])
        self.assertEqual("not a directory", path.read_text())

    def test_stream_bridge_parent_is_not_required_for_fcvm_metal(self):
        self.args.mode = "metal"
        self.host.path("/var/run/faas/stream").rmdir()
        report = self.collect()
        self.assertEqual("ready", report["preflight"])
        self.assertNotIn("stream_bridge_directory", [item["name"] for item in report["checks"]])

    def test_unknown_builder_capacity_expression_blocks(self):
        self.host.put("/repo/pkg/builderd/drive.go", "const BuildDriveMinFreeBytes = unknown\n")
        self.assertEqual("failed", self.check(self.collect(), "builder_drive_capacity")["status"])

    def test_scan_absence_mismatch_malformed_and_fixable_critical_block(self):
        original = self.scan.read_text()
        cases = [None, "not JSON", {"findings": {}},
                 {**json.loads(original), "source": "/other/base.ext4"},
                 {**json.loads(original), "image": "another image"},
                 {**json.loads(original), "fix_available_findings": {"CRITICAL": 1}},
                 {**json.loads(original), "fix_available_findings": {"CRITICAL": True}}]
        for content in cases:
            with self.subTest(content=content):
                if content is None:
                    self.scan.unlink()
                else:
                    self.scan.write_text(content if isinstance(content, str) else json.dumps(content))
                self.assertEqual("failed", self.check(self.collect(), "base/runner-builder-amd64.ext4")["status"])
                self.scan.write_text(original)

    def test_legacy_scan_fallback_keeps_strict_total_critical_gate(self):
        scan = json.loads(self.scan.read_text())
        for legacy in ("missing", None):
            scan.pop("fix_available_findings", None)
            if legacy is None:
                scan["fix_available_findings"] = None
            self.scan.write_text(json.dumps(scan))
            with self.assertRaises(ValueError):
                preflight.inspect_base(self.base, self.scan, self.digest)
        scan["findings"]["CRITICAL"] = 0
        self.scan.write_text(json.dumps(scan))
        preflight.inspect_base(self.base, self.scan, self.digest)

    def test_additional_runtime_bases_are_explicit_and_canonical(self):
        self.assertEqual("ready", self.collect()["preflight"])
        self.args.runtime_base_key = ["base/runner-node22-amd64.ext4"]
        self.assertEqual("blocked", self.collect()["preflight"])
        self.args.runtime_base_key = ["base/../secrets.ext4"]
        self.assertEqual("failed", self.check(self.collect(), "base_key")["status"])

    def test_remote_storage_does_not_require_local_artifacts(self):
        self.base.unlink()
        self.host.path("/srv/fc/kernel/1.7.0").unlink()
        with patch.dict(os.environ, {"FAAS_STORAGE_BACKEND": "oci"}):
            report = self.collect()
        self.assertEqual("ready", report["preflight"])
        self.assertEqual("unverified", self.check(report, "remote_storage_artifacts")["status"])

    def test_postgres_probe_is_read_only_and_credentials_are_not_reported(self):
        self.host.postgres = (1, "postgres://faas:secret@localhost/test")
        report = self.collect()
        argv, env = next(call for call in self.host.calls if call[0][-1] == "postgres-probe")
        self.assertEqual("postgres://faas:secret@localhost/test", env["FAAS_E2E_DATABASE_URL"])
        self.assertIn("has_schema_privilege(current_user,'public','USAGE')", preflight.POSTGRES_SQL)
        self.assertIn("OR (has_schema_privilege(current_user,'public','CREATE')", preflight.POSTGRES_SQL)
        self.assertNotIn("secret", " ".join(argv))
        self.assertTrue(preflight.POSTGRES_SQL.startswith("SELECT "))
        self.assertNotIn("secret", json.dumps(report))
        self.assertEqual("failed", self.check(report, "postgres_privileges")["status"])

    def test_blocked_cli_writes_evidence_and_safe_repair_without_running_tests(self):
        self.host.postgres = (1, "postgres://faas:secret@localhost/test")
        report_path = self.root / "evidence.json"
        stdout, stderr = io.StringIO(), io.StringIO()
        argv = [str(SCRIPT), "check", "--mode", "e2e", "--repo-root", "/repo",
                "--source-sha", self.args.source_sha, "--go", "go", "--kernel", self.args.kernel,
                "--fc-version", "1.7.0", "--report", str(report_path)]
        with patch("sys.argv", argv), patch("sys.stdout", stdout), patch("sys.stderr", stderr), \
                patch.object(preflight, "Host", return_value=self.host), \
                patch.object(preflight.stat, "S_ISCHR", return_value=True):
            self.assertEqual(1, preflight.main())
        self.assertNotIn("secret", stdout.getvalue() + stderr.getvalue() + report_path.read_text())
        self.assertIn("postgres_privileges", stderr.getvalue())
        self.assertEqual("not_run", json.loads(report_path.read_text())["acceptance"]["status"])

    def test_cgroup_source_and_toolchain_fail_before_tests(self):
        self.host.put("/sys/fs/cgroup/cgroup.subtree_control", "cpu")
        self.host.put("/repo/.faas-e2e-source-sha", "2" * 40)
        self.host.put("/repo/go.mod", "go 1.26.0\n")
        report = self.collect()
        for name in ("cgroup_v2_delegation", "source_archive", "pinned_go"):
            self.assertEqual("failed", self.check(report, name)["status"])

    def test_metal_fixture_does_not_require_platform_storage_or_database(self):
        self.args.mode = "metal"
        self.base.unlink()
        self.host.path("/srv/fc/kernel/1.7.0").unlink()
        self.assertEqual("ready", self.collect()["preflight"])
        self.assertNotIn("postgres-probe", [argv[-1] for argv, _ in self.host.calls])

    def test_libpq_expands_uri_and_overrides_writable_options_then_closes(self):
        library = MagicMock()
        library.PQconnectdbParams.return_value = 1
        library.PQstatus.return_value = 0
        library.PQexec.return_value = 2
        library.PQresultStatus.return_value = 2
        library.PQntuples.return_value = library.PQnfields.return_value = 1
        library.PQgetvalue.return_value = b"t"
        with patch.object(preflight.ct, "CDLL", return_value=library):
            self.assertTrue(preflight.postgres_probe("postgres://faas:secret@localhost/test?options=bad"))
        keywords, values, expand = library.PQconnectdbParams.call_args.args
        self.assertEqual(1, expand)
        self.assertEqual([b"dbname", b"connect_timeout", b"options", None], list(keywords))
        self.assertIn(b"secret", values[0])
        self.assertEqual(b"5", values[1])
        self.assertIn(b"default_transaction_read_only=on", values[2])
        self.assertIn(b"statement_timeout=5000", values[2])
        library.PQclear.assert_called_once_with(2)
        library.PQfinish.assert_called_once_with(1)
        library.reset_mock()
        library.PQstatus.return_value = 1
        with patch.object(preflight.ct, "CDLL", return_value=library):
            self.assertFalse(preflight.postgres_probe("postgres://invalid"))
        library.PQexec.assert_not_called()
        library.PQfinish.assert_called_once_with(1)

    def test_receipt_never_turns_skips_missing_results_or_cleanup_failure_green(self):
        report = self.collect()
        log = self.root / "results.log"
        cases = [
            ("--- PASS: TestBoot (1s)\n", 0, "passed", "passed"),
            ("--- PASS: TestBoot (1s)\n    --- SKIP: TestBoot/restore (0s)\n", 0, "passed", "incomplete"),
            ("--- PASS: TestBoot (1s)\n--- SKIP: TestFixture (0s)\n", 0, "passed", "incomplete"),
            ("--- PASS: TestBoot (1s)\n    --- FAIL: TestBoot/restore (1s)\n", 0, "passed", "failed"),
            ("--- PASS: TestBoot (1s)\n", 1, "passed", "failed"),
            ("--- PASS: TestBoot (1s)\n", 0, "failed", "failed"),
            ("PASS\n", 0, "passed", "incomplete"),
        ]
        for content, rc, cleanup, expected in cases:
            with self.subTest(expected=expected, content=content, rc=rc, cleanup=cleanup):
                log.write_text(content)
                result = preflight.finish(copy.deepcopy(report), [str(log)], rc, cleanup)
                self.assertEqual(expected, result["acceptance"]["status"])
                self.assertFalse(result["host"]["performance_qualified"])
        log.unlink()
        result = preflight.finish(report, [str(log)], 0, "passed")
        self.assertEqual("incomplete", result["acceptance"]["status"])
        self.assertEqual([str(log)], result["acceptance"]["missing_logs"])

    def test_native_detection_alone_cannot_qualify_performance(self):
        self.host.virtualization = (1, "none")
        report = self.collect()
        self.assertTrue(report["host"]["native_host"])
        self.assertFalse(report["host"]["performance_qualified"])

    def test_invalid_storage_backend_is_blocked_not_treated_as_remote(self):
        with patch.dict(os.environ, {"FAAS_STORAGE_BACKEND": "invalid"}):
            self.assertEqual("failed", self.check(self.collect(), "storage_backend")["status"])

    def test_runners_check_before_build_and_service_mutation_and_retain_reports(self):
        repo = SCRIPT.parents[2]
        for name in ("run-native-e2e.sh", "run-native-metal-smoke.sh"):
            script = SCRIPT.with_name(name).read_text()
            check = script.index('native-acceptance-preflight.py" check')
            for mutation in ('mkdir -p /var/lock', 'systemctl stop', ' build \\\n'):
                self.assertLess(check, script.index(mutation), (name, mutation))
            self.assertLess(script.index('native-acceptance-preflight.py" finish'), script.index('rm -rf "${stage_root}"'))
        for workflow, prefix in (("e2e-native.yml", "acceptance-e2e-"), ("builder-native.yml", "acceptance-metal-")):
            source = (repo / ".github/workflows" / workflow).read_text()
            self.assertLess(source.index(prefix + "*.json"), source.index("faas-canary-artifacts finish", source.index(prefix + "*.json")))


if __name__ == "__main__":
    unittest.main()
