from uuid import uuid4

from faas_sdk.api.apps import bulk_act_on_issues, compare_issue_releases
from faas_sdk.models.bulk_issue_action_request import BulkIssueActionRequest
from faas_sdk.models.issue_release_comparison import IssueReleaseComparison


def test_release_comparison_preserves_unknown_health_and_deployment_scope():
    baseline, candidate = uuid4(), uuid4()
    request = compare_issue_releases._get_kwargs("payments", baseline=baseline, candidate=candidate)
    assert request["params"] == {"baseline": str(baseline), "candidate": str(candidate), "environment": "application"}
    health = {
        "deployment_id": str(baseline),
        "commit_sha": "",
        "created_at": "2026-10-13T12:00:00Z",
        "observed_requests": 0,
        "http_5xx_requests": 0,
        "http_5xx_rate": None,
        "request_coverage": "unknown",
        "observed_issue_events": 0,
        "observed_issue_groups": 0,
        "new_issue_groups": 0,
        "regressed_issue_groups": 0,
        "identified_customers": 0,
        "unattributed_events": 0,
    }
    parsed = IssueReleaseComparison.from_dict(
        {
            "window_start": "2026-10-12T12:00:00Z",
            "window_end": "2026-10-13T12:00:00Z",
            "environment": "application",
            "baseline": health,
            "candidate": {**health, "deployment_id": str(candidate)},
            "http_5xx_rate_delta": None,
        }
    )
    assert parsed.candidate.http_5xx_rate is None and parsed.http_5xx_rate_delta is None
    assert parsed.to_dict()["http_5xx_rate_delta"] is None


def test_bulk_request_preserves_explicit_selection():
    ids = [str(uuid4()), str(uuid4())]
    payload = {"issue_ids": ids, "action": {"action": "reopen"}}
    body = BulkIssueActionRequest.from_dict(payload)
    request = bulk_act_on_issues._get_kwargs("payments", body=body)
    assert request["url"] == "/v1/apps/payments/issues/bulk-actions"
    assert request["json"] == payload
