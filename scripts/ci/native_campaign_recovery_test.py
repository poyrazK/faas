"""adr: 984 — portable crash-drill contracts; no native qualification claim."""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import unittest
from unittest.mock import Mock

import native_campaign_recovery as drill


class RecoveryContracts(unittest.TestCase):
    def test_pidfd_pins_actual_worker_and_rejects_reused_identity(self):
        child = subprocess.Popen(["cat"], stdin=subprocess.PIPE, stdout=subprocess.DEVNULL)
        try:
            identity = {"pid": child.pid, "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
                        "pid_namespace": os.readlink("/proc/self/ns/pid"), "start_ticks": drill.process_start(child.pid)}
            record = {"state": "started", "process": identity}
            fd = drill.pin_worker(record)
            try:
                identity["start_ticks"] = "0"
                with self.assertRaisesRegex(ValueError, "reused"):
                    drill.pin_worker(record)
                signal.pidfd_send_signal(fd, signal.SIGKILL)
                self.assertEqual(child.wait(timeout=5), -signal.SIGKILL)
            finally:
                os.close(fd)
        finally:
            if child.poll() is None:
                child.kill()
                child.wait(timeout=5)
            child.stdin.close()

    def test_foreign_identity_cannot_be_signalled(self):
        with self.assertRaisesRegex(ValueError, "another host"):
            drill.pin_worker({"state": "started", "process": {
                "pid": os.getpid(), "boot_id": "foreign", "pid_namespace": "foreign"}})

    def test_uncertain_reservation_and_no_report_required(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root)
            checkpoint = {"halted": True, "batches": {"a": {"status": "uncertain"}}}
            path = directory / "checkpoint.json"
            path.write_text(json.dumps(checkpoint))
            drill.verify_uncertain(directory, "a")
            (directory / "batch-a.json").write_text("{}")
            with self.assertRaisesRegex(ValueError, "gained a report"):
                drill.verify_uncertain(directory, "a")
            (directory / "batch-a.json").unlink()
            checkpoint["batches"]["a"]["status"] = "completed"
            path.write_text(json.dumps(checkpoint))
            with self.assertRaisesRegex(ValueError, "reservation"):
                drill.verify_uncertain(directory, "a")

    def test_replacement_session_blocks_crash_barrier(self):
        api = Mock()
        journal = {"run_id": "a" * 32, "workloads": {"app": {"project": "dedicated", "app_id": "owned"}}}
        api.get.return_value = (200, {"app": {"id": "owned"}})
        drill.inspect_sessions(api, journal)
        self.assertEqual(api.get.call_args.args[0], "/v1/dev/sessions/dedicated?workspace_id=" + "a" * 32)
        for response in ((404, None), (200, {"app": {"id": "replacement"}})):
            api.get.return_value = response
            with self.assertRaisesRegex(ValueError, "differs"):
                drill.inspect_sessions(api, journal)


if __name__ == "__main__":
    unittest.main()
