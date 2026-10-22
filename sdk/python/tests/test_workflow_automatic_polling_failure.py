import pytest

from faas_sdk.models.workflow_recovery_policy_automatic_execution import WorkflowRecoveryPolicyAutomaticExecution


@pytest.mark.parametrize(
    "code", [None, "invalid_policy_authority", "invalid_polling_data", "invalid_recovery_identity"]
)
def test_automatic_polling_failure_and_legacy_execution_round_trip(code):
    body = {"consecutive_failures": 0, "pause_reason": ""}
    if code is not None:
        body["polling_failure"] = {
            "code": code,
            "attempts": 2,
            "failed_at": "2026-10-13T18:00:00+00:00",
            "retry_at": "2026-10-13T18:02:00+00:00",
        }
    parsed = WorkflowRecoveryPolicyAutomaticExecution.from_dict(body)
    assert parsed.to_dict() == body
    assert parsed.consecutive_failures == 0
    assert "paused_at" not in parsed.to_dict()
