"""ADR-570: guard test inventory completeness and coverage merging."""
import importlib.util
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("state_suite", pathlib.Path(__file__).with_name("state-suite.py"))
suite = importlib.util.module_from_spec(spec)
spec.loader.exec_module(suite)


class StateSuiteTest(unittest.TestCase):
    def test_partition_includes_tests_examples_and_fuzz(self):
        names = ["TestB", "TestA", "ExampleC", "FuzzD", "BenchmarkE"]
        groups = suite.partitions(names)
        self.assertEqual(set(groups[0]) | set(groups[1]), set(names[:-1]))
        self.assertFalse(set(groups[0]) & set(groups[1]))

    def test_refuses_duplicate_or_unknown_inventory(self):
        for names in [["TestA", "TestA"], ["TestA", "unexpected output"], []]:
            with self.assertRaises(ValueError):
                suite.partitions(names)

    def test_inventory_artifact_retains_stdout_and_stderr_separately(self):
        names = [f"Test{index}" for index in range(16)]
        stdout = "\n".join(names) + "\n"
        stderr = "warning: GOCOVERDIR not set, no coverage data emitted\n"
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            result = subprocess.CompletedProcess([], 0, stdout, stderr)
            with mock.patch.object(suite.subprocess, "run", return_value=result):
                inventory = suite.list_inventory(output / "state.test", output, {}, output)
            retained = (output / "inventory.log").read_text().splitlines()
            self.assertEqual(suite.partitions(retained, 8), suite.partitions(inventory, 8))
            self.assertEqual(set().union(*map(set, suite.partitions(retained, 8))), set(names))
            self.assertEqual((output / "inventory.stderr").read_text(), stderr)

    def test_inventory_failure_retains_both_streams_and_unknown_stdout_refuses(self):
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            result = subprocess.CompletedProcess([], 2, "TestA\n", "inventory failed\n")
            with mock.patch.object(suite.subprocess, "run", return_value=result):
                with self.assertRaises(ValueError):
                    suite.list_inventory(output / "state.test", output, {}, output)
            self.assertEqual((output / "inventory.log").read_text(), result.stdout)
            self.assertEqual((output / "inventory.stderr").read_text(), result.stderr)
            result = subprocess.CompletedProcess([], 0, "TestA\nunknown stdout\n", "")
            with mock.patch.object(suite.subprocess, "run", return_value=result):
                inventory = suite.list_inventory(output / "state.test", output, {}, output)
            with self.assertRaises(ValueError):
                suite.partitions(inventory)

    def test_eight_partitions_remain_exhaustive(self):
        names = [f"Test{index}" for index in range(32)]
        groups = suite.partitions(names, 8)
        self.assertEqual(set().union(*map(set, groups)), set(names))
        self.assertEqual(sum(map(len, groups)), len(names))
        for count in [0, 1, 33]:
            with self.assertRaises(ValueError):
                suite.partitions(names, count)

    def test_refuses_missing_failed_or_duplicate_terminals(self):
        suite.validate_terminals(["TestA", "TestB"], "--- PASS: TestA (0s)\n--- SKIP: TestB (0s)\n")
        for log in ["--- PASS: TestA (0s)\n", "--- FAIL: TestA (0s)\n--- PASS: TestB (0s)\n", "--- PASS: TestA (0s)\n--- PASS: TestA (0s)\n--- PASS: TestB (0s)\n"]:
            with self.assertRaises(ValueError):
                suite.validate_terminals(["TestA", "TestB"], log)

    def merge(self, first, second, other="mode: atomic\nother.go:1.1,2.1 2 1\n"):
        with tempfile.TemporaryDirectory() as directory:
            paths = [pathlib.Path(directory) / name for name in ["first", "second", "other"]]
            for path, value in zip(paths, [first, second, other]):
                path.write_text(value)
            return suite.merged_coverage(paths[:2], paths[2])

    def test_merges_counters_without_doubling_statements(self):
        merged = self.merge("mode: atomic\na.go:1.1,2.1 3 0\n", "mode: atomic\na.go:1.1,2.1 3 2\n")
        self.assertEqual(merged, "mode: atomic\na.go:1.1,2.1 3 2\nother.go:1.1,2.1 2 1\n")

    def test_refuses_truncated_or_changed_coverage(self):
        first = "mode: atomic\na.go:1.1,2.1 3 1\n"
        for second in ["mode: atomic\n", "mode: atomic\nb.go:1.1,2.1 3 1\n", "mode: atomic\na.go:1.1,2.1 4 1\n"]:
            with self.assertRaises(ValueError):
                self.merge(first, second)


if __name__ == "__main__":
    unittest.main()
