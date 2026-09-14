#!/usr/bin/env python3
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deploy/ansible/roles/canary_artifact_retention/files/faas-canary-artifacts"
LOADER = importlib.machinery.SourceFileLoader("faas_canary_artifacts", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
LOADER.exec_module(MODULE)


class CanaryArtifactManagerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.artifacts = self.root / "artifacts"
        self.artifacts.mkdir()
        self.state = self.root / "state"
        self.metrics = self.root / "metrics" / "artifacts.prom"
        self.proc = self.root / "proc"
        self.proc.mkdir()
        self.config = self.root / "policy.json"
        self.config.write_text(
            json.dumps(
                {
                    "retention_seconds": 100,
                    "active_lease_seconds": 50,
                    "pressure_grace_seconds": 10,
                    "max_bytes": 1024 * 1024,
                    "allowed_patterns": [str(self.artifacts / "canary-*")],
                }
            ),
            encoding="utf-8",
        )
        self.systemctl = self._systemctl("exit 0")
        self.clock = 1_000.0

    def tearDown(self):
        self.temp.cleanup()

    def _systemctl(self, body):
        script = self.root / f"systemctl-{len(list(self.root.glob('systemctl-*')))}"
        script.write_text(f"#!/bin/sh\n{body}\n", encoding="utf-8")
        script.chmod(0o755)
        return script

    def manager(self, systemctl=None):
        return MODULE.Manager(
            str(self.config),
            str(self.state),
            str(self.metrics),
            str(self.proc),
            str(systemctl or self.systemctl),
            now=lambda: self.clock,
        )

    def artifact(self, name="canary-one", content=b"payload"):
        path = self.artifacts / name
        path.mkdir()
        (path / "data").write_bytes(content)
        return path

    def test_register_records_owner_host_creation_time_and_active_state(self):
        path = self.artifact()
        manager = self.manager()
        manager.register(str(path), "123-2", "native-e2e")

        record = json.loads(manager.record_path(path).read_text(encoding="utf-8"))
        self.assertEqual(record["path"], str(path))
        self.assertEqual(record["run_id"], "123-2")
        self.assertEqual(record["created_at"], self.clock)
        self.assertEqual(record["state"], "active")
        self.assertTrue(record["host"])
        metrics = self.metrics.read_text(encoding="utf-8")
        self.assertIn("faas_canary_artifact_count 1", metrics)
        self.assertIn("faas_canary_artifact_bytes ", metrics)

    def test_successful_and_failed_finalizers_remove_artifacts(self):
        manager = self.manager()
        for index, state in enumerate(("success", "failure"), start=1):
            path = self.artifact(f"canary-{index}")
            manager.register(str(path), f"run-{index}", "canary")
            manager.finish(str(path), state)
            self.assertFalse(path.exists())
            self.assertFalse(manager.record_path(path).exists())
        self.assertEqual(manager.counters()["cleanup_total"], 2)
        self.assertIn("faas_canary_artifact_count 0", self.metrics.read_text(encoding="utf-8"))

    def test_sweep_preserves_live_lease_and_reaps_abandoned_active_record(self):
        path = self.artifact()
        manager = self.manager()
        manager.register(str(path), "run", "canary")
        manager.sweep()
        self.assertTrue(path.exists())

        self.clock += 51
        manager.sweep()
        self.assertFalse(path.exists())
        self.assertIn(
            "faas_canary_artifact_last_sweep_timestamp_seconds 1051",
            self.metrics.read_text(encoding="utf-8"),
        )

    def test_disk_pressure_never_breaks_an_active_lease(self):
        policy = json.loads(self.config.read_text(encoding="utf-8"))
        policy["max_bytes"] = 1
        self.config.write_text(json.dumps(policy), encoding="utf-8")
        path = self.artifact(content=b"x" * 200)
        manager = self.manager()
        manager.register(str(path), "run", "canary")
        self.clock += 20
        manager.sweep()
        self.assertTrue(path.exists())

    def test_sweep_discovers_old_unregistered_declared_artifact(self):
        path = self.artifact()
        os.utime(path, (self.clock - 101, self.clock - 101))
        self.manager().sweep()
        self.assertFalse(path.exists())

    def test_byte_cap_removes_oldest_unreferenced_artifact_after_grace(self):
        policy = json.loads(self.config.read_text(encoding="utf-8"))
        policy["max_bytes"] = 100
        policy["retention_seconds"] = 10_000
        self.config.write_text(json.dumps(policy), encoding="utf-8")
        old = self.artifact("canary-old", b"x" * 200)
        new = self.artifact("canary-new", b"x" * 200)
        os.utime(old, (self.clock - 30, self.clock - 30))
        os.utime(new, (self.clock - 5, self.clock - 5))
        self.manager().sweep()
        self.assertFalse(old.exists())
        self.assertTrue(new.exists(), "pressure grace protects a just-created unregistered workspace")

    def test_active_systemd_reference_defers_finalizer(self):
        path = self.artifact()
        systemctl = self._systemctl(
            f'''if [ "$1" = "list-units" ]; then
  echo "canary.service loaded active running canary"
else
  echo "ExecStart={{ path=/bin/sh ; argv[]=/bin/sh {path}/run.sh ; }}"
fi'''
        )
        manager = self.manager(systemctl)
        manager.register(str(path), "run", "canary")
        manager.finish(str(path), "success")
        self.assertTrue(path.exists())
        record = json.loads(manager.record_path(path).read_text(encoding="utf-8"))
        self.assertEqual(record["state"], "completed")

    def test_active_systemd_dropin_reference_defers_finalizer(self):
        path = self.artifact()
        dropin = self.root / "active.conf"
        dropin.write_text(f"Environment=CANARY_ROOT={path}\n", encoding="utf-8")
        systemctl = self._systemctl(
            f'''if [ "$1" = "list-units" ]; then
  echo "canary.service loaded active running canary"
else
  echo "DropInPaths={dropin}"
fi'''
        )
        manager = self.manager(systemctl)
        manager.register(str(path), "run", "canary")
        manager.finish(str(path), "failure")
        self.assertTrue(path.exists())

    def test_open_process_reference_defers_cleanup(self):
        path = self.artifact()
        process = self.proc / "999999"
        process.mkdir()
        (process / "cwd").symlink_to(path)
        manager = self.manager()
        manager.register(str(path), "run", "canary")
        manager.finish(str(path), "failure")
        self.assertTrue(path.exists())

    def test_process_that_vanishes_during_procfs_read_does_not_block_cleanup(self):
        path = self.artifact()
        os.utime(path, (self.clock - 101, self.clock - 101))
        process = self.proc / "2"
        process.mkdir()
        original = MODULE.pathlib.Path.read_bytes

        def read_bytes(candidate):
            if candidate == process / "environ":
                raise ProcessLookupError("process vanished")
            return original(candidate)

        with mock.patch.object(MODULE.pathlib.Path, "read_bytes", read_bytes):
            self.manager().sweep()
        self.assertFalse(path.exists())

    def test_failed_reference_proof_keeps_artifact_and_increments_failure(self):
        path = self.artifact()
        manager = self.manager(self._systemctl("exit 1"))
        manager.register(str(path), "run", "canary")
        with self.assertRaises(MODULE.ReferenceProofError):
            manager.finish(str(path), "success")
        self.assertTrue(path.exists())
        self.assertEqual(manager.counters()["cleanup_failures_total"], 1)

    def test_sweep_propagates_failed_reference_proof_after_writing_metrics(self):
        path = self.artifact()
        os.utime(path, (self.clock - 101, self.clock - 101))
        manager = self.manager(self._systemctl("exit 1"))

        with self.assertRaises(MODULE.ReferenceProofError):
            manager.sweep()

        self.assertTrue(path.exists())
        self.assertEqual(manager.counters()["cleanup_failures_total"], 1)
        self.assertIn(
            "faas_canary_artifact_cleanup_failures_total 1",
            self.metrics.read_text(encoding="utf-8"),
        )

    def test_systemd_unit_starting_with_dash_uses_option_terminator(self):
        path = self.artifact()
        os.utime(path, (self.clock - 101, self.clock - 101))
        systemctl = self._systemctl(
            '''if [ "$1" = "list-units" ]; then
  echo "-.mount loaded active mounted Root Mount"
  exit 0
fi
for arg in "$@"; do
  if [ "$arg" = "--" ]; then
    exit 0
  fi
done
exit 64'''
        )

        self.manager(systemctl).sweep()

        self.assertFalse(path.exists())

    def test_closed_policy_rejects_outside_and_symlink_paths(self):
        outside = self.root / "outside"
        outside.mkdir()
        manager = self.manager()
        with self.assertRaises(MODULE.PolicyError):
            manager.register(str(outside), "run", "canary")
        target = self.root / "target"
        target.mkdir()
        symlink = self.artifacts / "canary-link"
        symlink.symlink_to(target)
        with self.assertRaises(MODULE.PolicyError):
            manager.register(str(symlink), "run", "canary")


if __name__ == "__main__":
    unittest.main(verbosity=2)
