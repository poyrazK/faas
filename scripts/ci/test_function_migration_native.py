"""Portable rejection tests. No mocked result here qualifies a native host."""

import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest import mock


def load_driver():
    path = pathlib.Path(__file__).with_name("function-migration-native.py")
    spec = importlib.util.spec_from_file_location("function_migration_native", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


DRIVER = load_driver()


def fixture_config():
    return {"version": 1, "fixture_id": "11111111-1111-4111-8111-111111111111",
            "catalog": "faas_function_migration_native_11111111111141118111111111111111",
            "api_url": "http://127.0.0.1:18080", "gateway_url": "http://127.0.0.1:18081",
            "app_domain": "fixture.example", "sql_ip": "1.1.1.2", "sql_ca_path": "/fixture/ca.pem", "fc_version": "test-only",
            "hosts": [{"node_id": node, "ssh": "root@fixture-" + node, "remote_socket": "/run/faas/vmmd.sock",
                       "assets": {name: "/fixture/" + name for name in DRIVER.ASSETS}}
                      for node in ("22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")]}


class GuardTests(unittest.TestCase):
    def test_fixture_rejects_wrong_catalog_host_alias_and_unsafe_targets(self):
        bad = []
        for field, value in (("catalog", "faas"), ("api_url", "http://example.com:80"),
                             ("gateway_url", "http://127.0.0.1:80/redirect"), ("sql_ip", "169.254.169.254")):
            c = fixture_config()
            c[field] = value
            bad.append(c)
        c = fixture_config()
        c["hosts"][1]["node_id"] = c["hosts"][0]["node_id"]
        bad.append(c)
        c = fixture_config()
        c["hosts"][0]["ssh"] = "-oProxyCommand=unwanted"
        bad.append(c)
        c = fixture_config()
        c["hosts"][0]["remote_socket"] = "/run/faas/../other.sock"
        bad.append(c)
        with tempfile.TemporaryDirectory() as work:
            path = pathlib.Path(work) / "config.json"
            path.write_text(json.dumps(fixture_config()))
            self.assertEqual(DRIVER.load_config(path), fixture_config())
            for config in bad:
                with self.subTest(config=config):
                    path.write_text(json.dumps(config))
                    with self.assertRaises(ValueError):
                        DRIVER.load_config(path)

    def test_catalog_requires_explicit_verified_tls_and_exact_database(self):
        c = fixture_config()
        base = "postgres://fixture:private@localhost/" + c["catalog"]
        DRIVER.check_catalog(c, base + "?sslmode=verify-full")
        for dsn in (base, base + "?sslmode=require", base + "?sslmode=verify-full&sslmode=disable",
                    "postgres://fixture:private@localhost/faas?sslmode=verify-full"):
            with self.subTest(dsn=dsn), self.assertRaises(ValueError):
                DRIVER.check_catalog(c, dsn)

    def test_skipped_missing_and_failed_cases_cannot_pass_gate(self):
        root = "TestFunctionMigrationDestinationPostgresMetal"
        cases = [root + "/" + runtime + "/" + case for runtime in ("node22", "node24") for case in DRIVER.SCENARIOS]
        complete = [{"Action": "pass", "Test": name} for name in (*cases, root + "/node22", root + "/node24", root)] + [{"Action": "pass"}]
        with tempfile.TemporaryDirectory() as work:
            path = pathlib.Path(work) / "test-events.jsonl"
            path.write_text("\n".join(map(json.dumps, complete)))
            DRIVER.check_test_results(path)
            for events in ([], complete[1:], complete[:-1], complete + [{"Action": "skip", "Test": root + "/node24"}],
                           complete + [{"Action": "fail"}]):
                path.write_text("\n".join(map(json.dumps, events)))
                with self.subTest(events=events), self.assertRaises(ValueError):
                    DRIVER.check_test_results(path)

    def test_wrong_source_or_release_claim_cannot_reuse_report(self):
        c = fixture_config()
        # Only the report envelope is exercised here. Go verifies its physical,
        # SQL and worker witnesses before the real binary may write a report.
        report = {"version": DRIVER.REPORT_VERSION, "scope": DRIVER.REPORT_SCOPE, "config": c,
                  "both_hosts_leak_checked": True, "failure_matrix_qualified": False, "production_ready": False,
                  "runs": [{"runtime": runtime, "scenario": case} for runtime in ("node22", "node24") for case in DRIVER.SCENARIOS]}
        with tempfile.TemporaryDirectory() as work:
            path = pathlib.Path(work) / "report.json"
            path.write_text(json.dumps(report))
            DRIVER.check_report(path, c)
            for field, value in (("config", {}), ("production_ready", True), ("failure_matrix_qualified", True),
                                 ("both_hosts_leak_checked", False), ("version", 1), ("scope", "native-successful-migration-v1"),
                                 ("version", 2), ("scope", "native-migration-reply-boundaries-v1"),
                                 ("runs", report["runs"][:-1]), ("runs", [report["runs"][0]] * len(report["runs"]))):
                bad = copy.deepcopy(report)
                bad[field] = value
                path.write_text(json.dumps(bad))
                with self.subTest(field=field), self.assertRaises(ValueError):
                    DRIVER.check_report(path, c)

    def test_unprivileged_coordinator_never_builds_or_contacts_hosts(self):
        with mock.patch.object(DRIVER.os, "geteuid", return_value=1000), mock.patch.object(DRIVER, "remote") as remote:
            with self.assertRaisesRegex(ValueError, "requires root"):
                DRIVER.run("absent-fixture", "absent-output")
            remote.assert_not_called()


if __name__ == "__main__":
    unittest.main()
