#!/usr/bin/env python3
"""Check owned-container cleanup on uncertain startup and test failure."""
import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("dependency", Path(__file__).with_name("scenario-dependency-postgres.py"))
dependency = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dependency)


class CleanupTests(unittest.TestCase):
    def test_lost_startup_receipt_and_sql_failure_remove_exact_owned_name(self):
        for mode in ("startup-timeout", "tls-failure", "sql-failure", "success"):
            with self.subTest(mode=mode):
                created = []
                removed = []
                directories = []
                def output(command, **kwargs):
                    if "run" in command:
                        created.append(command[command.index("--name") + 1])
                        self.assertIn("POSTGRES_PASSWORD", command)
                        self.assertFalse(any(item.startswith("POSTGRES_PASSWORD=") for item in command))
                        if mode == "startup-timeout":
                            raise subprocess.TimeoutExpired(command, 120)
                        return "owned-container-id\n"
                    return "54321\n"
                def tls_setup(_docker, _container, _environment, directory):
                    directories.append(directory)
                    if mode == "tls-failure":
                        raise subprocess.CalledProcessError(1, "tls-fixture")
                def run(command, **kwargs):
                    if "rm" in command:
                        removed.append(command[-1])
                    if command[0] == "node" and mode == "sql-failure":
                        raise subprocess.CalledProcessError(1, command)
                    return subprocess.CompletedProcess(command, 0)
                with patch.object(dependency, "setup_tls", side_effect=tls_setup), patch.object(dependency.subprocess, "run", side_effect=run), patch.object(dependency.subprocess, "check_output", side_effect=output), patch.object(dependency.signal, "signal"), contextlib.redirect_stdout(io.StringIO()):
                    if mode == "success":
                        dependency.main()
                    else:
                        with self.assertRaises(subprocess.SubprocessError):
                            dependency.main()
                self.assertTrue(all(not Path(directory).exists() for directory in directories))
                self.assertEqual(removed, created)
                self.assertEqual(len(removed), 1)
                self.assertTrue(removed[0].startswith("gregale-write-pg-"))

    def test_failed_removal_cannot_pass_if_owned_container_remains(self):
        def run(command, **kwargs):
            return subprocess.CompletedProcess(command, 1 if "rm" in command else 0)
        def output(command, **kwargs):
            if "run" in command:
                return "owned-container-id\n"
            if "inspect" in command:
                return "54321\n"
            return "owned-container-still-present\n"
        with patch.object(dependency, "setup_tls"), patch.object(dependency.subprocess, "run", side_effect=run), patch.object(dependency.subprocess, "check_output", side_effect=output), patch.object(dependency.signal, "signal"), contextlib.redirect_stdout(io.StringIO()), self.assertRaises(RuntimeError):
            dependency.main()


if __name__ == "__main__":
    unittest.main()
