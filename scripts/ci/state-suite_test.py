"""ADR-375: guard test inventory completeness and coverage merging."""
import importlib.util
import pathlib
import tempfile
import unittest

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
