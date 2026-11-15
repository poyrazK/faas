import httpx

from faas_sdk.preview.api.events import get_workflow_recovery_report
from faas_sdk.preview.models.workflow_recovery_report import WorkflowRecoveryReport


def test_recovery_report_preserves_coverage_and_omitted_timing():
    totals = {
        "attempts": 2,
        "succeeded": 0,
        "failed": 0,
        "cancelled": 0,
        "other_results": 0,
        "without_observed_result": 2,
        "pauses": 1,
        "timed_results": 0,
    }
    body = {
        "observed_at": "2026-10-11T00:00:00+00:00",
        "created_after": "2026-10-04T00:00:00+00:00",
        "created_before": "2026-10-11T00:00:00+00:00",
        "scanned_entries": 10000,
        "durable_entries": 9900,
        "truncated": True,
        "coverage_notes": ["retained evidence only"],
        "totals": totals,
        "groups": [{"workflow": "orders", "policy_version": 2, "totals": totals}],
        "blockers": [{"reason": "hourly_budget", "actions": 2, "evidence": []}],
        "evidence": [],
    }
    assert WorkflowRecoveryReport.from_dict(body).to_dict() == body
    response = httpx.Response(200, json=body)
    assert (
        get_workflow_recovery_report._parse_response(
            client=type("Client", (), {"raise_on_unexpected_status": True})(), response=response
        ).to_dict()
        == body
    )
    kwargs = get_workflow_recovery_report._get_kwargs("consumer", workflow="orders", policy_version=2)
    assert kwargs["url"] == "/v1/apps/consumer/recovery-policy/report"
    assert kwargs["params"]["workflow"] == "orders"
    assert kwargs["params"]["policy_version"] == 2
    totals["mean_retry_result_seconds"] = 4.5
    totals["timed_results"] = 1
    assert WorkflowRecoveryReport.from_dict(body).totals.mean_retry_result_seconds == 4.5
