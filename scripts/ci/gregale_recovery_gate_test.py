"""Contract tests for gate failure propagation and acceptance-test accounting."""
import importlib.util
import json
import re
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("recovery_gate", Path(__file__).with_name("gregale_recovery_gate.py"))
GATE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GATE)


class RecoveryGateEventsTest(unittest.TestCase):
    def events(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "events.json"
            path.write_text("\n".join(json.dumps(event) for event in events) + "\n")
            return GATE.test_events(path)

    def passed(self):
        return [{"Action": "pass", "Test": name} for name in sorted(GATE.REQUIRED)]

    def test_all_acceptance_tests_must_pass(self):
        self.assertEqual([], self.events(self.passed())["errors"])

    def test_no_matching_tests_fails(self):
        self.assertTrue(self.events([{"Action": "pass", "Package": "cli"}])["missing_acceptance_tests"])

    def test_skips_are_not_success(self):
        events = self.passed() + [{"Action": "skip", "Test": "TestCampaignRecoveryNeedsInfrastructure"}]
        self.assertTrue(self.events(events)["errors"])

    def test_package_failure_is_not_masked_by_passed_tests(self):
        events = self.passed() + [{"Action": "fail", "Package": "cli"}]
        self.assertEqual(["cli"], self.events(events)["failed"])

    def test_filter_selects_recovery_without_shell_completion_dependencies(self):
        for name in GATE.REQUIRED | {"TestRecoveryCompletionSessionAbsenceDoesNotCompleteMissingTest"}:
            self.assertIsNotNone(re.search(GATE.FILTER, name))
        self.assertIsNone(re.search(GATE.FILTER, "TestCompletion_FishScriptIsSyntacticallyValid"))
        self.assertIsNone(re.search(GATE.FILTER, "TestCompletion_ZshScriptIsSyntacticallyValid"))

    def test_postgres_acceptance_requires_both_race_orders_and_restarts(self):
        required = GATE.POSTGRES_REQUIRED
        self.assertEqual(14, len(required))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "events.json"
            path.write_text(json.dumps({"Action": "pass", "Test": "TestScenarioConsumerPostgresPortableRecovery"}))
            self.assertEqual(13, len(GATE.test_events(path, required)["missing_acceptance_tests"]))
            path.write_text("\n".join(json.dumps({"Action": "pass", "Test": name}) for name in required))
            self.assertFalse(GATE.test_events(path, required)["errors"])

    def test_malformed_json_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "events.json"
            path.write_text("not JSON\n")
            self.assertTrue(GATE.test_events(path)["errors"])


if __name__ == "__main__":
    unittest.main()
