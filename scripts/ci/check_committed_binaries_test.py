#!/usr/bin/env python3
"""Tests for check_committed_binaries.py."""

import importlib.util
import pathlib
import struct
import subprocess
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location(
    "check_committed_binaries", pathlib.Path(__file__).with_name("check_committed_binaries.py"))
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)


def pe_header() -> bytes:
    head = bytearray(b"MZ" + b"\0" * 126)
    head[60:64] = struct.pack("<I", 64)
    head[64:68] = b"PE\0\0"
    return bytes(head)


class ExecutableKindTests(unittest.TestCase):
    def test_detects_executable_headers(self):
        cases = {
            "ELF": b"\x7fELF\x02\x01\x01",
            "Mach-O": b"\xcf\xfa\xed\xfe\x0c\x00\x00\x01",
            "Mach-O universal": b"\xca\xfe\xba\xbe\x00\x00\x00\x02",
            "PE": pe_header(),
        }
        for kind, head in cases.items():
            with self.subTest(kind=kind):
                self.assertEqual(check.executable_kind(head), kind)

    def test_ignores_non_executables(self):
        cases = {
            "text": b"#!/usr/bin/env bash\necho hi\n",
            "java class": b"\xca\xfe\xba\xbe\x00\x00\x00\x41",
            "MZ text": b"MZ is just two letters" + b" " * 64,
            "empty": b"",
        }
        for name, head in cases.items():
            with self.subTest(name=name):
                self.assertIsNone(check.executable_kind(head))


class ViolationTests(unittest.TestCase):
    def test_reports_tracked_executables_outside_the_allowlist(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "tool").write_bytes(b"\xcf\xfa\xed\xfe" + b"\0" * 60)
            (root / "addon.node").write_bytes(b"\x7fELF" + b"\0" * 60)
            (root / "README.md").write_text("docs\n")
            (root / "untracked-binary").write_bytes(b"\x7fELF" + b"\0" * 60)
            subprocess.run(["git", "-C", str(root), "add", "tool", "addon.node", "README.md"], check=True)
            found = check.violations(root, {"addon.node": "reproducible build"})
            self.assertEqual(found, ["tool: Mach-O executable"])


if __name__ == "__main__":
    unittest.main()
