import contextlib
import io
import unittest
from types import SimpleNamespace

from lint_go_shard import lint_packages, select_packages


class LintShardTests(unittest.TestCase):
    def test_inventory_is_checked_exactly_once_across_all_shards(self):
        packages = [f"example.test/project/pkg/p{i:03}" for i in range(324)]
        selected = [target for shard in range(1, 5)
                    for target in select_packages(packages, "example.test/project", shard, 4)]
        self.assertCountEqual(selected, ["./pkg/p" + f"{i:03}" for i in range(324)])
        self.assertEqual(len(selected), len(set(selected)))
        self.assertEqual(select_packages(packages, "example.test/project", 1, 4),
                         select_packages(list(reversed(packages)), "example.test/project", 1, 4))

    def test_bad_inventory_or_shard_cannot_silently_skip_lint(self):
        for packages, shard, shards in [([], 1, 4), (["other.test/pkg"], 1, 4),
                                        (["example.test/project"], 0, 4),
                                        (["example.test/project"], 5, 4),
                                        (["example.test/project"], 1, 0)]:
            with self.subTest(packages=packages, shard=shard, shards=shards):
                with self.assertRaises(ValueError):
                    select_packages(packages, "example.test/project", shard, shards)

    def test_failure_is_retained_and_later_packages_still_checked(self):
        calls = []

        def run(command, check):
            calls.append(command)
            self.assertFalse(check)
            return SimpleNamespace(returncode=1 if command[-1] == "./bad" else 0)

        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            result = lint_packages(["go", "tool", "golangci-lint"], ["./first", "./bad", "./last"], run)
        self.assertEqual(result, 1)
        self.assertEqual([command[-1] for command in calls], ["./first", "./bad", "./last"])
        self.assertTrue(all(command[:5] == ["go", "tool", "golangci-lint", "run", "--timeout=20m"] for command in calls))


if __name__ == "__main__":
    unittest.main()
