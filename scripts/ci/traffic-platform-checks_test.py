"""ADR-570: prove CI refuses incomplete and skipped traffic evidence."""

import importlib.util
import json
import pathlib
import re
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("traffic_checks", pathlib.Path(__file__).with_name("traffic-platform-checks.py"))
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)


class TrafficEvidenceTest(unittest.TestCase):
    def test_committed_selector_runs_all_required_postgres_tests(self):
        scope = json.loads(pathlib.Path(__file__).with_name("traffic-platform-scope.json").read_text())
        module = pathlib.Path(__file__).resolve().parents[2].joinpath("go.mod").read_text().splitlines()[0].split()[1]
        packages = {module + package[1:] for package in scope["packages"]}
        selector = re.compile(scope["postgres_selector"])
        for fixture in scope["required_postgres_tests"]:
            with self.subTest(fixture=fixture):
                self.assertIn(fixture["package"], packages)
                self.assertRegex(fixture["test"], selector)

    def judge(self, events, required=(("gateway", "TestPG"),)):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "tests.jsonl"
            path.write_text("".join(json.dumps(event) + "\n" for event in events))
            return checks.summarize_log(path, ["gateway", "state"], required)

    def good(self):
        return [{"Action": "pass", "Package": "gateway", "Test": "TestPG"},
                {"Action": "pass", "Package": "gateway"},
                {"Action": "pass", "Package": "state", "Test": "TestState"},
                {"Action": "pass", "Package": "state"}]

    def test_complete_evidence(self):
        result = self.judge(self.good())
        self.assertEqual(result["accepted_named_passes"], 2)
        self.assertEqual(result["postgres_fixture_named_results"], 1)

    def test_missing_fixture(self):
        with self.assertRaisesRegex(ValueError, "missing/skipped"):
            self.judge([event for event in self.good() if event.get("Test") != "TestPG"])

    def test_skipped_fixture(self):
        events = self.good()
        events[0]["Action"] = "skip"
        with self.assertRaisesRegex(ValueError, "missing/skipped"):
            self.judge(events)

    def test_skipped_child_of_passing_fixture(self):
        events = self.good() + [{"Action": "skip", "Package": "gateway", "Test": "TestPG/source"}]
        with self.assertRaisesRegex(ValueError, "missing/skipped"):
            self.judge(events)

    def test_truncated_package(self):
        with self.assertRaisesRegex(ValueError, "incomplete package"):
            self.judge(self.good()[:-1])

    def test_failed_event(self):
        events = self.good() + [{"Action": "fail", "Package": "gateway", "Test": "TestPG"}]
        with self.assertRaisesRegex(ValueError, "failed test/package"):
            self.judge(events)

    def test_guarded_unit_parent_is_not_acceptance(self):
        events = self.good() + [{"Action": "pass", "Package": "gateway", "Test": "TestGuarded"},
                                {"Action": "skip", "Package": "gateway", "Test": "TestGuarded/native"}]
        result = self.judge(events, ())
        self.assertEqual(result["accepted_named_passes"], 2)
        self.assertEqual(result["guarded_or_skipped"], 2)


if __name__ == "__main__":
    unittest.main()
