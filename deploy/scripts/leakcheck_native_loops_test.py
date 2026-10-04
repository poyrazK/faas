#!/usr/bin/env python3
"""ADR-567: unreadable loop evidence cannot pass the shell leak gate."""

import contextlib
import errno
import importlib.util
import io
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "native_loop_gate", Path(__file__).with_name("leakcheck_native_loops.py")
)
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class NativeLoopGateTest(unittest.TestCase):
    def run_gate(self):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            status = gate.main()
        return status, output.getvalue()

    def test_missing_inventory_cannot_report_zero(self):
        with patch.object(gate.Path, "iterdir", side_effect=FileNotFoundError("missing kernel inventory")):
            status, output = self.run_gate()
        self.assertEqual(status, 1)
        self.assertIn("cannot inspect kernel loop inventory", output)

    def test_loop_without_mount_is_visible_in_fixed_kernel_abi(self):
        devices = [Path("/sys/block/loop0"), Path("/sys/block/loop1"), Path("/sys/block/loop2")]

        def ioctl(descriptor, command, info, mutate):
            self.assertEqual(command, 0x4C05)
            self.assertEqual(len(info), 232)
            self.assertTrue(mutate)
            if descriptor == 10:
                raise OSError(errno.ENXIO, "unconfigured")
            marker = b"/var/lib/system.img" if descriptor == 11 else b"gregale-loop:orphan"
            info[56:56 + len(marker)] = marker

        with patch.object(gate.Path, "iterdir", return_value=iter(devices)), \
                patch.object(gate.os, "open", side_effect=[10, 11, 12]), \
                patch.object(gate.os, "close") as close, \
                patch.object(gate.fcntl, "ioctl", side_effect=ioctl):
            status, output = self.run_gate()
        self.assertEqual(status, 1)
        self.assertIn("native loop attachment loop2", output)
        self.assertNotIn("loop0", output)
        self.assertNotIn("loop1", output)
        self.assertEqual(close.call_count, 3)

    def test_permission_denied_is_failure(self):
        with patch.object(gate.Path, "iterdir", return_value=iter([Path("/sys/block/loop7")])), \
                patch.object(gate.os, "open", side_effect=PermissionError(errno.EACCES, "permission denied")):
            status, output = self.run_gate()
        self.assertEqual(status, 1)
        self.assertIn("cannot inspect native loop loop7", output)


if __name__ == "__main__":
    unittest.main()
