from faas_sdk.api.workflows.start_automation_live_verification import _get_kwargs
from faas_sdk.models.automation_live_expectation import AutomationLiveExpectation
from faas_sdk.models.automation_live_verification import AutomationLiveVerification
from faas_sdk.models.automation_test_case import AutomationTestCase
from faas_sdk.models.start_automation_live_verification_request import StartAutomationLiveVerificationRequest


def test_live_expectations_preserve_null_zero_and_large_numbers():
    for value in [
        {"step": "main", "output": None, "attempt_count": 0},
        {"step": "main", "output": {"id": 9007199254740993}, "state": "succeeded"},
        {"step": "main", "state": "succeeded"},
    ]:
        assert AutomationLiveExpectation.from_dict(value).to_dict() == value
    case = {
        "name": "synthetic",
        "simulation": {"input": {"id": 9007199254740993}},
        "expectations": [{"step": "main", "state": "mocked"}],
        "live": {"expectations": [{"step": "main", "state": "succeeded", "output": None}]},
    }
    assert AutomationTestCase.from_dict(case).to_dict() == case


def test_live_start_requires_explicit_key_and_target_fences():
    body = {
        "expected_version": 7,
        "expected_published_version": 6,
        "expected_deployment_id": "compact-deployment-id",
        "case_name": "synthetic",
        "confirm_real_actions": True,
    }
    request = StartAutomationLiveVerificationRequest.from_dict(body)
    assert request.to_dict() == body
    kwargs = _get_kwargs("billing", "report", body=request, idempotency_key="stable-live-key")
    assert kwargs["headers"]["Idempotency-Key"] == "stable-live-key"
    assert kwargs["url"] == "/v1/apps/billing/automations/report/live-verifications"
    assert kwargs["json"] == body
    report = {
        "run_id": "018e7310-2222-4333-8444-555555555555",
        "case_name": "synthetic",
        "deployment_id": "compact-deployment-id",
        "inputs_hash": "a" * 64,
        "run_status": "succeeded",
        "version": 7,
        "published_version": 6,
        "created_at": "2026-10-15T10:00:00+00:00",
        "outdated": False,
        "report_current": True,
        "report": {
            "checked_at": "2026-10-15T10:00:00+00:00",
            "passed": False,
            "run_status": "succeeded",
            "resume_count": 0,
            "run_updated_at": "2026-10-15T10:00:00+00:00",
            "issue": "assertion_failed",
            "steps": [{"step": "main", "passed": False, "state": "succeeded", "mismatches": ["output"]}],
        },
    }
    assert AutomationLiveVerification.from_dict(report).to_dict() == report
