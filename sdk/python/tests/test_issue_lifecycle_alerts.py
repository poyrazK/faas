from faas_sdk.api.apps import (
    get_issue_lifecycle_alert_policy,
    preview_issue_lifecycle_alert,
    set_issue_lifecycle_alert_policy,
)
from faas_sdk.models.issue_lifecycle_alert_policy import IssueLifecycleAlertPolicy
from faas_sdk.models.issue_lifecycle_alert_preview import IssueLifecycleAlertPreview
from faas_sdk.models.preview_issue_lifecycle_alert_request import PreviewIssueLifecycleAlertRequest
from faas_sdk.models.update_issue_lifecycle_alert_policy_request import UpdateIssueLifecycleAlertPolicyRequest


def test_lifecycle_policy_retains_revision_and_empty_environment_selection():
    payload = {"expected_revision": 2, "environments": [], "cooldown_seconds": 0}
    body = UpdateIssueLifecycleAlertPolicyRequest.from_dict(payload)
    kwargs = set_issue_lifecycle_alert_policy._get_kwargs("payments", body=body)
    assert kwargs["method"] == "put"
    assert kwargs["url"] == "/v1/apps/payments/issue-lifecycle-alert-policy"
    assert kwargs["json"] == payload
    assert get_issue_lifecycle_alert_policy._get_kwargs("payments")["url"] == kwargs["url"]
    policy = IssueLifecycleAlertPolicy.from_dict({"revision": 3, "environments": [], "cooldown_seconds": 0})
    assert policy.to_dict() == {"revision": 3, "environments": [], "cooldown_seconds": 0}


def test_lifecycle_preview_retains_hypothetical_event_and_expiry():
    payload = {
        "issue_id": "28ad5060-28e6-4ba5-ac6a-9990691c11c3",
        "event": "regressed",
        "environments": ["production"],
        "cooldown_seconds": 60,
    }
    body = PreviewIssueLifecycleAlertRequest.from_dict(payload)
    kwargs = preview_issue_lifecycle_alert._get_kwargs("payments", body=body)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/apps/payments/issue-lifecycle-alert-policy/preview"
    assert kwargs["json"] == payload
    preview = IssueLifecycleAlertPreview.from_dict(
        {
            "issue_id": "28ad5060-28e6-4ba5-ac6a-9990691c11c3",
            "event": "regressed",
            "environment": "production",
            "policy_revision": 3,
            "allowed": False,
            "reason": "cooldown",
            "next_eligible_at": "2026-10-17T14:01:00Z",
        }
    )
    assert not preview.allowed
    assert preview.reason == "cooldown"
    assert preview.next_eligible_at.isoformat() == "2026-10-17T14:01:00+00:00"
