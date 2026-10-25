from faas_sdk.preview.models.automation_response import AutomationResponse
from faas_sdk.preview.models.automation_test_report import AutomationTestReport
from faas_sdk.preview.models.save_automation_test_suite_request import SaveAutomationTestSuiteRequest


def test_saved_suite_null_and_large_integer_roundtrip():
    suite = {
        "sample": {"input": {"id": 9007199254740993}},
        "cases": [
            {
                "name": "explicit null",
                "simulation": {"mock_outputs": {"main": None}},
                "expectations": [{"step": "main", "output": None}],
            }
        ],
    }
    for body in [{"expected_version": 7, "suite": suite}, {"expected_version": 7, "suite": None}]:
        assert SaveAutomationTestSuiteRequest.from_dict(body).to_dict() == body
    report = {
        "checked_version": 7,
        "checked_at": "2026-10-15T10:00:00+00:00",
        "inputs_hash": "a" * 64,
        "passed": False,
        "cases": [
            {
                "name": "explicit null",
                "passed": False,
                "issue": "assertion_failed",
                "steps": [
                    {"step": "main", "state": "mocked", "passed": False, "mismatches": ["output", "attempt_count"]}
                ],
            }
        ],
    }
    assert AutomationTestReport.from_dict(report).to_dict() == report
    response = {
        "name": "tests",
        "version": 7,
        "source": "draft",
        "draft": {"name": "tests", "steps": []},
        "enabled": True,
        "test_suite": suite,
        "test_report": report,
    }
    assert AutomationResponse.from_dict(response).to_dict() == response
