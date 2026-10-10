"""Regression fixtures for profiles produced by multiple test binaries."""
import contextlib
import io
import re
from pathlib import Path
import subprocess
import tempfile
import unittest

import coverage_floor


class CoverageFloorTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def profile(self, name, lines, mode="atomic"):
        path = self.root / name
        path.write_text(f"mode: {mode}\n" + "\n".join(lines) + "\n")
        return str(path)

    def block(self, file_, statements, hits, position="1.1,2.1"):
        return f"{coverage_floor.REPO_PREFIX}{file_}:{position} {statements} {hits}"

    def test_complementary_hits_union_within_and_across_profiles(self):
        first = self.profile("first.out", [
            self.block("pkg/state/store.go", 70, 1),
            self.block("pkg/state/store.go", 30, 0, "3.1,4.1"),
            self.block("pkg/state/store.go", 70, 0),
        ])
        second = self.profile("second.out", [
            self.block("pkg/state/store.go", 70, 0),
            self.block("pkg/state/store.go", 30, 9, "3.1,4.1"),
        ])
        blocks = coverage_floor.read_blocks([first, second])
        self.assertEqual(len(blocks), 2)
        self.assertEqual(coverage_floor.package_stats(blocks)["pkg/state"], [100, 100])

    def test_exact_state_excludes_subpackages_generated_and_tests(self):
        path = self.profile("scope.out", [
            self.block("pkg/state/store.go", 70, 1),
            self.block("pkg/state/store.go", 30, 0, "3.1,4.1"),
            self.block("pkg/state/sqlc/generated.go", 1000, 1),
            self.block("pkg/state/conformance/helper.go", 1000, 1),
            self.block("pkg/state/store_test.go", 1000, 1),
            self.block("pkg/stateful/unrelated.go", 1000, 1),
        ])
        blocks = coverage_floor.read_blocks([path])
        self.assertEqual(coverage_floor.package_stats(blocks, exact_state=True)["pkg/state"], [100, 70])
        self.assertEqual(coverage_floor.package_stats(blocks)["pkg/state"], [1100, 1070])

    def test_make_state_gate_uses_union_and_unchanged_floor(self):
        path = self.profile("state.out", [
            self.block("pkg/state/store.go", 70, 1),
            self.block("pkg/state/store.go", 30, 0, "3.1,4.1"),
            self.block("pkg/state/store.go", 70, 0),
            self.block("pkg/state/store.go", 30, 0, "3.1,4.1"),
        ])
        result = subprocess.run(
            ["make", "check-state-coverage", f"COVERFILE={path}"],
            cwd=Path(__file__).resolve().parents[2],
            capture_output=True, text=True, check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("70.0% ✓", result.stdout)

    def test_general_floor_unions_duplicate_uncovered_blocks(self):
        path = self.profile("sched.out", [
            self.block("pkg/sched/scheduler.go", 60, 1),
            self.block("pkg/sched/scheduler.go", 40, 0, "3.1,4.1"),
            self.block("pkg/sched/scheduler.go", 60, 0),
            self.block("pkg/sched/scheduler.go", 40, 0, "3.1,4.1"),
        ])
        with contextlib.redirect_stdout(io.StringIO()) as output:
            result = coverage_floor.main([path])
        self.assertEqual(result, 0)
        self.assertIn("pkg/sched: 60.0% (floor ≥ 60%) ✓", output.getvalue())

    def test_rounded_percentage_cannot_pass_below_threshold(self):
        path = self.profile("below.out", [
            self.block("pkg/state/store.go", 6996, 1),
            self.block("pkg/state/store.go", 3004, 0, "3.1,4.1"),
        ])
        with contextlib.redirect_stdout(io.StringIO()) as output:
            result = coverage_floor.main([path], state_only=True)
        self.assertEqual(result, 1)
        self.assertIn("70.0% ✗", output.getvalue())

    def test_missing_exact_state_statements_fail(self):
        path = self.profile("generated.out", [self.block("pkg/state/sqlc/generated.go", 100, 1)])
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(coverage_floor.main([path], state_only=True), 1)

    def test_conflicting_statement_counts_fail(self):
        path = self.profile("conflict.out", [
            self.block("pkg/state/store.go", 2, 0),
            self.block("pkg/state/store.go", 3, 1),
        ])
        with self.assertRaisesRegex(ValueError, "statement counts differ"):
            coverage_floor.read_blocks([path])

    def test_malformed_or_incompatible_profiles_fail(self):
        malformed = self.profile("bad.out", ["not a coverage block"])
        with self.assertRaisesRegex(ValueError, "invalid coverage block"):
            coverage_floor.read_blocks([malformed])
        missing = self.root / "missing-mode.out"
        missing.write_text(self.block("pkg/state/store.go", 1, 1) + "\n")
        with self.assertRaisesRegex(ValueError, "invalid coverage mode"):
            coverage_floor.read_blocks([str(missing)])
        first = self.profile("atomic.out", [self.block("pkg/state/store.go", 1, 1)])
        second = self.profile("set.out", [self.block("pkg/state/store.go", 1, 1)], mode="set")
        with self.assertRaisesRegex(ValueError, "coverage modes differ"):
            coverage_floor.read_blocks([first, second])


class StateCoverageWorkflowTest(unittest.TestCase):
    """Exact-package coverage must include actual API adapter invocations."""

    def test_complete_state_and_api_profiles_are_required(self):
        workflow = (Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml").read_text()
        jobs = dict(re.findall(r"^  ([a-z][a-z0-9-]*):\n(.*?)(?=^  [a-z][a-z0-9-]*:\n|\Z)", workflow, re.M | re.S))
        api_job = jobs["unit-tests-pg-1"]
        self.assertIn("STATEPKG=$(go list ./pkg/state)", api_job)
        self.assertIn('"$PKGS" "$STATEPKG"', api_job)
        self.assertIn('-coverpkg="$COVERPKGS"', api_job)
        self.assertIn("normalize_go_cover_profile.py coverage/cover-shard1.out", api_job)
        self.assertIn("name: coverage-state-api", api_job)
        upload = api_job.split("- name: upload public API state-adapter coverage", 1)[1].split("- name: sdk-check", 1)[0]
        self.assertIn("if-no-files-found: error", upload)
        self.assertNotRegex(upload, r"(?m)^\s+if:")
        aggregate = jobs["state-coverage"]
        self.assertIn("needs: [unit-tests-state, unit-tests-pg-2, unit-tests-pg-1]", aggregate)
        command = next(line for line in aggregate.splitlines() if "coverage_floor.py --state-only" in line)
        profiles = command.split("--state-only", 1)[1].split()
        self.assertEqual(set(profiles), {
            *(f"coverage/cover-shard-state-{shard}.out" for shard in range(1, 5)),
            "coverage/cover-shard2a.out", "coverage/cover-shard1.out",
        })
        self.assertEqual(len(profiles), 6)


if __name__ == "__main__":
    unittest.main()
