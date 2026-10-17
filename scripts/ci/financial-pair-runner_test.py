#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import re
import shlex
import sys
import tempfile
import time
import unittest

spec = importlib.util.spec_from_file_location("runner", Path(__file__).with_name("financial-pair-runner.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class RunnerTest(unittest.TestCase):
    def test_ci_budget_covers_serial_commands_and_restoration(self):
        workflow = (runner.ROOT / ".github/workflows/financial-pair-native.yml").read_text()
        match = re.search(r"timeout-minutes:\s*(\d+)", workflow)
        self.assertIsNotNone(match)
        budget = int(match[1]) * 60
        commands = 2 * runner.BUILD_TIMEOUT + runner.TRANSPORT_BUILD_TIMEOUT + 6 * runner.FILESYSTEM_TIMEOUT + runner.SINGLE_HOST_TIMEOUT + runner.PEER_NETWORK_TIMEOUT + runner.NATIVE_TIMEOUT
        # Setup/upload, both host joins, retirement, eight evidence reads,
        # both readiness waits, and fixture transfer must fit as well.
        overhead = 20 * 60 + 2 * 180 + 2 * 120 + 8 * 20 + 2 * 90 + 180
        self.assertGreaterEqual(budget, commands + overhead)
        self.assertLessEqual(budget, 360 * 60)

    def test_jail_namespace_leaves_room_for_native_sockets(self):
        first = runner.jail_root("a" * 40, "long-reviewed-run-identifier")
        self.assertRegex(first, r"^/srv/f[a-f0-9]{6}$")
        self.assertLessEqual(len(first), 12)
        self.assertNotEqual(first, runner.jail_root("a" * 40, "another-run"))

    def test_ssh_preserves_literal_arguments(self):
        args = ["sudo", "bash", "a path/with 'quotes'", "$(must-stay-literal); true"]
        command = runner.remote("fixture-peer", args)
        self.assertEqual(shlex.split(command[-1]), args)
        self.assertIn("BatchMode=yes", command)
        self.assertIn("ServerAliveCountMax=3", command)

    def test_live_hold_receives_heartbeat_and_finishes(self):
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "hold.log"
            code = 'import sys\nfor line in sys.stdin:\n print(line.strip(), flush=True)\n if line.strip() == "finish": sys.exit(0)\nsys.exit(7)\n'
            hold = runner.Hold([sys.executable, "-u", "-c", code], log)
            until = time.monotonic() + 5
            while "alive" not in log.read_text() and time.monotonic() < until:
                time.sleep(0.01)
            self.assertEqual(hold.finish(), 0)
            self.assertEqual(log.read_text().splitlines(), ["alive", "finish"])
            self.assertFalse(hold.writer.is_alive())

    def test_failed_host_hold_keeps_its_actual_status(self):
        with tempfile.TemporaryDirectory() as temporary:
            hold = runner.Hold([sys.executable, "-c", "raise SystemExit(7)"], Path(temporary) / "hold.log")
            hold.process.wait(timeout=5)
            self.assertEqual(hold.finish(), 7)

    def test_command_timeout_is_failure_and_keeps_diagnostic(self):
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "command.log"
            code = "import time; time.sleep(30)"
            self.assertEqual(runner.run([sys.executable, "-c", code], log, timeout=0.05), 124)
            self.assertIn("deadline expired", log.read_text())


if __name__ == "__main__":
    unittest.main()
