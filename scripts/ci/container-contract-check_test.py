#!/usr/bin/env python3
"""Regression checks for the strict container qualification verifier."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location(
    "container_contract_check", Path(__file__).with_name("container-contract-check.py"))
GATE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GATE)
UDP_SPEC = importlib.util.spec_from_file_location(
    "udp_contract_check", Path(__file__).with_name("udp-contract-check.py"))
UDP_GATE = importlib.util.module_from_spec(UDP_SPEC)
UDP_SPEC.loader.exec_module(UDP_GATE)


class ContractGateTests(unittest.TestCase):
    def test_udp_discovery_includes_valid_signature_shapes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "go.mod").write_text("module example\n")
            package = root / "pkg/contracts"
            package.mkdir(parents=True)
            (package / "contract_test.go").write_text(
                "func TestOtherName(check *testing.T) {}\n"
                "func TestMultiline(\n check *testing.T,\n) {}\n"
                "func TestUnnamed(*testing.T) {}\n")
            with patch.object(UDP_GATE, "ROOT", root), patch.object(UDP_GATE, "FILES", {"pkg/contracts": "*_test.go"}):
                expected = UDP_GATE.required_tests()
            self.assertEqual(expected, {("example/pkg/contracts", name) for name in (
                "TestOtherName", "TestMultiline", "TestUnnamed")})

    def test_discovery_includes_valid_signature_shapes_and_external_tests(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            package = root / "pkg/contracts"
            package.mkdir(parents=True)
            (package / "contract_test.go").write_text(
                "func TestOrdinary(t *testing.T) {}\n"
                "func TestOtherName(check *testing.T) {}\n"
                "func TestMultiline(\n check *testing.T,\n) {}\n"
                "func TestUnnamed(*testing.T) {}\n"
                "func TestNotATest(value string) {}\n")
            (package / "external_test.go").write_text("func TestExternal(t *testing.T) {}\n")
            result = subprocess.CompletedProcess([], 0, json.dumps({
                "Dir": str(package), "ImportPath": "example/contracts",
                "TestGoFiles": ["contract_test.go"], "XTestGoFiles": ["external_test.go"],
            }), "")
            with patch.object(GATE, "ROOT", root), patch.object(GATE, "PACKAGES", {"pkg/contracts": r"Test\w+"}), \
                    patch.object(GATE.subprocess, "run", return_value=result):
                expected = GATE.required_tests("go")
            self.assertEqual(expected, {("example/contracts", name) for name in (
                "TestOrdinary", "TestOtherName", "TestMultiline", "TestUnnamed", "TestExternal")})

    def test_discovery_failure_is_fatal(self):
        result = subprocess.CompletedProcess([], 1, "", "source setup failed\n")
        with patch.object(GATE.subprocess, "run", return_value=result), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, "source discovery failed"):
                GATE.required_tests("go")

    def test_verdict_requires_pass_and_rejects_skips_failures_and_process_errors(self):
        identity = {"Package": "example/contracts", "Test": "TestContract"}
        cases = (
            ("pass", [{**identity, "Action": "pass"}], 0, 0),
            ("missing", [], 0, 1),
            ("skipped", [{**identity, "Action": "skip"}], 0, 1),
            ("subtest skipped", [{**identity, "Action": "pass"},
                                 {**identity, "Test": "TestContract/child", "Action": "skip"}], 0, 1),
            ("package failed", [{**identity, "Action": "pass"},
                                {"Package": "example/contracts", "Action": "fail"}], 0, 1),
            ("process failed", [{**identity, "Action": "pass"}], 1, 1),
        )
        for name, events, exit_code, expected in cases:
            with self.subTest(name=name):
                process = unittest.mock.MagicMock()
                process.__enter__.return_value = process
                process.stdout = io.StringIO("".join(json.dumps(event) + "\n" for event in events))
                process.wait.return_value = exit_code
                with patch.object(GATE, "required_tests", return_value={("example/contracts", "TestContract")}), \
                        patch.object(GATE.subprocess, "Popen", return_value=process), \
                        contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                    self.assertEqual(GATE.main(), expected)


if __name__ == "__main__":
    unittest.main()
