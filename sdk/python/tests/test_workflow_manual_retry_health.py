import pytest

from faas_sdk.models.workflow_recovery_overview import WorkflowRecoveryOverview


@pytest.mark.parametrize("include_retry", [False, True])
def test_manual_retry_health_optional_coverage(include_retry):
    body = {
        "status": "waiting",
        "monitoring_status": "no_observed_failure",
        "observed_at": "2026-10-14T12:00:00+00:00",
        "monitoring_only": True,
        "manual_awaiting_approval": 0,
        "manual_unresolved": 0,
        "manual_polling_failures": 0,
        "manual_attention": 0,
        "truncated": False,
        "issues": [],
    }
    if include_retry:
        ident = "11111111-1111-4111-8111-111111111111"
        body.update(manual_retry_awaiting_review=1, manual_retry_unresolved=2)
        body["issues"] = [
            {
                "scope": "manual_retry",
                "reason": "review_inspection_required",
                "policy_version": 3,
                "review_id": ident,
                "inspection_url": "/v1/apps/orders/recovery-policy/reviews/" + ident + "/status",
            }
        ]
    parsed = WorkflowRecoveryOverview.from_dict(body)
    assert parsed.to_dict() == body
    if include_retry:
        assert parsed.manual_retry_awaiting_review == 1
        assert parsed.manual_retry_unresolved == 2
