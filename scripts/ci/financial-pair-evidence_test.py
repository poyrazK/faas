#!/usr/bin/env python3
"""Portable rejection contracts; synthetic hosts never count as acceptance."""
import copy
import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from datetime import datetime, timedelta, timezone

spec = importlib.util.spec_from_file_location("pair", Path(__file__).with_name("financial-pair-evidence.py"))
pair = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pair)
traffic_spec = importlib.util.spec_from_file_location("traffic", Path(__file__).with_name("financial-pair-traffic.py"))
traffic = importlib.util.module_from_spec(traffic_spec)
traffic_spec.loader.exec_module(traffic)


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
        for name in traffic.SUPPORT:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("synthetic contract source\n")
        # Preserve the pair component catalog while adding all traffic phases.
        (self.root / traffic.SOURCE).write_text("\n".join(
            "func " + name + "(t *testing.T) { " + "; ".join('t.Run("' + phase + '", fn)' for phase in phases) + " }"
            for name, phases in (
                ("TestMetalFinancialPairGatewayProtocols", sorted(traffic.PROTOCOLS)),
                ("TestMetalFinancialPairGatewayFailureBounds", ["exhaustion", "private_isolation", "deadline"]),
                ("TestMetalFinancialPairGatewayOwnerRestart", ["before_crash", "retired_attempt", "new_attempt"]),
            )) + "\n")
        component, _ = pair.source_tests(self.root)
        tests, _ = pair.source_tests(self.root, pair=True)
        output = []
        for name, phases in {**component, **tests}.items():
            output.extend(f"    --- PASS: {name}/{phase} (0.1s)" for phase in phases)
            output.append(f"--- PASS: {name} (1.0s)")
        output.append("PASS")
        assets = {name: "c" * 64 for name in pair.ASSETS}
        pair.write_json(self.records / "fixture.json", {"assets": assets, "archive_sha256": "f" * 64, "traffic_host_b": "192.0.2.2"})
        identities = {}
        for index, node in enumerate(("a", "b")):
            directory = self.records / ("host-" + node)
            directory.mkdir()
            (directory / "leakcheck.log").write_text("synthetic successful cleanup\n")
            host = {"source_sha": self.sha, "run_id": self.run_id, "root": True, "kvm": True, "architecture": "x86_64", "machine_id_sha256": str(index + 1) * 64, "boot_id_sha256": str(index + 3) * 64, "assets": assets, "tools": {name: "d" * 64 for name in ("firecracker", "jailer")}}
            pair.write_json(directory / "ready.json", host)
            identities[node] = {**host, "architecture": "amd64"}
            pair.write_json(directory / "cleanup.json", {**host, "runner_exit": 0, "leakcheck_exit": 0, "post_repair_exit": 0, "restore_exit": 0, "leakcheck_sha256": pair.digest(directory / "leakcheck.log")})
            owner = {"node": index, "instance": f"fixture-{index}", "host": {**host, "architecture": "amd64"}}
            output.append("test.go:1: financial-pair-owner:" + json.dumps(owner))
        now = datetime.now(timezone.utc)
        for name in sorted(traffic.CASES):
            retired = name in {"deadline", "retired_attempt"}
            group = "protocol" if name in traffic.PROTOCOLS else "outage" if name in {"exhaustion", "private_isolation", "deadline"} else "new" if name == "new_attempt" else "restart"
            number = {"protocol": "1", "outage": "2", "restart": "3", "new": "4"}[group]
            identifier = number * 8 + "-" + number * 4 + "-" + number * 4 + "-" + number * 4 + "-" + number * 12
            before = 100 + 100 * sorted(traffic.PROTOCOLS).index(name) if name in traffic.PROTOCOLS else 100 if name in {"before_crash", "new_attempt", "exhaustion"} else 200
            after = before + 100 if name in traffic.PROTOCOLS else 200
            value = {
                "case": name, "gateway_host": identities["a"], "owner_host": identities["b"],
                "Node": "a" * 8 + "-" + "a" * 4 + "-" + "a" * 4 + "-" + "a" * 4 + "-" + "a" * 12,
                "Instance": identifier, "LeaseID": identifier,
                "Incarnation": "after" if name == "retired_attempt" else "new" if name == "new_attempt" else "before",
                "Veth": "vhtest", "RPCTarget": "tcp://192.0.2.2:54321",
                "Payload": 0 if retired else 64, "Before": before, "After": 0 if retired else after,
                "BlockedPayload": {name: 0 for name in ("h2c_grpc", "upgrade", "tcp", "udp")} if name == "exhaustion" else None,
                "InterfaceIndex": 11 if name == "new_attempt" else 9,
                "Funded": 1024, "RetryPayload": 0, "StableAfter": 200,
                "ReservedMillicents": 0, "CommittedMillicents": 1, "LimitMillicents": 10,
                "PhysicalAbsent": retired, "DatabaseUnavailable": group == "outage", "CounterAvailable": not retired,
                "ObservedAt": now.isoformat(), "FundedUntil": (now + timedelta(seconds=30)).isoformat(),
                "EgressReserved": 0 if name == "retired_attempt" else 1024, "EgressCommitted": 1024 if name == "retired_attempt" else 0,
                "ReceiptRevision": 1, "ReplayRevision": 1 if name == "retired_attempt" else 0,
                "Settled": name == "retired_attempt", "UnconfirmedLeases": 0 if name == "retired_attempt" else 1,
            }
            output.append("test.go:1: financial-pair-traffic:" + json.dumps(value))
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
        hashes = pair.traffic_sources(self.root)
        for path in [self.records / "fixture.json", *(self.records / ("host-" + node) / name for node in ("a", "b") for name in ("ready.json", "cleanup.json"))]:
            data = json.loads(path.read_text())
            data["traffic_sources"] = hashes
            pair.write_json(path, data)

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

    def change_traffic(self, case, mutate):
        path = self.records / "native.log"
        original = path.read_text()
        changed = []
        for line in original.splitlines():
            if "financial-pair-traffic:" in line:
                prefix, raw = line.split("financial-pair-traffic:", 1)
                value = json.loads(raw)
                if value["case"] == case:
                    mutate(value)
                    line = prefix + "financial-pair-traffic:" + json.dumps(value)
            changed.append(line)
        path.write_text("\n".join(changed) + "\n")
        self.assertFalse(self.report()["qualified"], case)
        path.write_text(original)

    def test_gateway_measurements_cannot_be_missing_duplicate_or_malformed(self):
        path = self.records / "native.log"
        original = path.read_text()
        entry = next(line for line in original.splitlines() if '"case": "tcp"' in line)
        for bad in (original.replace(entry + "\n", ""), original + entry + "\n", original + "financial-pair-traffic:invalid json\n"):
            path.write_text(bad)
            self.assertFalse(self.report()["qualified"])
        path.write_text(original)

    def test_gateway_accounting_requires_canonical_bounded_integers(self):
        for field in traffic.INTEGER_FIELDS:
            for value in (True, "64", 1.5, -1, 2**63):
                with self.subTest(field=field, value=value):
                    self.change_traffic("tcp", lambda data: data.update({field: value}))

    def test_gateway_meter_and_money_bounds_are_mandatory(self):
        for field, value in (("Payload", 101), ("After", 228), ("Funded", 199), ("Before", 201), ("CounterAvailable", False), ("PhysicalAbsent", True), ("Veth", ""), ("ReservedMillicents", 10), ("CommittedMillicents", 11)):
            with self.subTest(field=field):
                self.change_traffic("tcp", lambda data: data.update({field: value}))

    def test_gateway_exhaustion_private_and_deadline_cannot_reopen_authority(self):
        for protocol in ("h2c_grpc", "upgrade", "tcp", "udp"):
            self.change_traffic("exhaustion", lambda data: data["BlockedPayload"].update({protocol: 1}))
        for invalid in (None, {}, {"tcp": 0}, {name: False for name in ("h2c_grpc", "upgrade", "tcp", "udp")}):
            self.change_traffic("exhaustion", lambda data: data.update(BlockedPayload=invalid))
        for case, field, value in (
            ("exhaustion", "RetryPayload", 1), ("exhaustion", "StableAfter", 199),
            ("exhaustion", "DatabaseUnavailable", False), ("private_isolation", "After", 201),
            ("private_isolation", "Payload", 0), ("private_isolation", "Before", 199),
            ("deadline", "After", 1), ("deadline", "Payload", 1), ("deadline", "PhysicalAbsent", False),
            ("deadline", "Settled", True), ("deadline", "ObservedAt", "2099-01-01T00:00:00Z"),
        ):
            with self.subTest(case=case, field=field):
                self.change_traffic(case, lambda data: data.update({field: value}))

    def test_gateway_restart_requires_new_authority_and_exact_settlement(self):
        for case, field, value in (
            ("retired_attempt", "Settled", False), ("retired_attempt", "UnconfirmedLeases", 1),
            ("retired_attempt", "EgressReserved", 1), ("retired_attempt", "EgressCommitted", 1023),
            ("retired_attempt", "ReplayRevision", 2), ("retired_attempt", "Incarnation", "before"),
            ("new_attempt", "Incarnation", "before"),
        ):
            with self.subTest(case=case, field=field):
                self.change_traffic(case, lambda data: data.update({field: value}))
        old = "33333333-3333-3333-3333-333333333333"
        for field in ("Instance", "LeaseID"):
            self.change_traffic("new_attempt", lambda data: data.update({field: old}))

    def test_live_gateway_authority_and_interface_cannot_be_released_or_reset(self):
        for field, value in (("Settled", True), ("UnconfirmedLeases", 0), ("EgressReserved", 1023), ("InterfaceIndex", 0), ("ObservedAt", 1), ("owner_host", []), ("RPCTarget", 7)):
            self.change_traffic("tcp", lambda data: data.update({field: value}))
        self.change_traffic("tcp", lambda data: data.update(Before=100, After=200))
        self.change_traffic("tcp", lambda data: data.update(InterfaceIndex=10))
        self.change_traffic("new_attempt", lambda data: data.update(InterfaceIndex=9))

    def test_gateway_must_reach_attested_peer_over_tcp(self):
        for key in ("gateway_host", "owner_host"):
            for field, value in (("machine_id_sha256", "0" * 64), ("boot_id_sha256", "0" * 64), ("root", False), ("kvm", False), ("architecture", "arm64")):
                self.change_traffic("tcp", lambda data: data[key].update({field: value}))
        for target in ("unix:///tmp/local.sock", "tcp://127.0.0.1:54321", "tcp://192.0.2.2:0", "tcp://192.0.2.3:54321", "tcp://peer.invalid:54321"):
            self.change_traffic("tcp", lambda data: data.update(RPCTarget=target))

    def test_gateway_supporting_source_and_all_bridge_assets_are_bound(self):
        for relative in traffic.SUPPORT:
            path = self.root / relative
            original = path.read_text()
            path.write_text(original + "changed\n")
            self.assertFalse(self.report()["qualified"], relative)
            path.write_text(original)
        self.change_json("fixture.json", lambda data: data.update(traffic_sources={}))
        self.change_json("host-b/cleanup.json", lambda data: data.update(traffic_sources={}))
        for bridge in ("vmmd-stream-bridge", "vmmd-raw-bridge", "vmmd-tcp-bridge", "vmmd-udp-bridge", "financialguest", "traffic.ext4"):
            self.change_json("host-b/ready.json", lambda data: data["assets"].pop(bridge))


if __name__ == "__main__":
    unittest.main()
