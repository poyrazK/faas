from faas_sdk.preview.api.apps import (
    get_issue_compound_alert_policy,
    preview_issue_compound_alert,
    set_issue_compound_alert_policy,
)
from faas_sdk.preview.models.preview_issue_compound_alert_request import PreviewIssueCompoundAlertRequest
from faas_sdk.preview.models.update_issue_compound_alert_policy_request import UpdateIssueCompoundAlertPolicyRequest


def test_compound_policy_and_preview_keep_both_thresholds_and_one_window():
    rule = {
        "key": "payments", "enabled": True, "environment": "production",
        "minimum_events": 20, "minimum_customers": 3,
        "window_seconds": 600, "cooldown_seconds": 1800,
    }
    payload = {"expected_revision": 2, "rules": [rule]}
    request = UpdateIssueCompoundAlertPolicyRequest.from_dict(payload)
    kwargs = set_issue_compound_alert_policy._get_kwargs("payments", body=request)
    assert kwargs["json"] == payload
    assert kwargs["method"] == "put"
    assert kwargs["url"] == "/v1/apps/payments/issue-compound-alert-policy"
    assert get_issue_compound_alert_policy._get_kwargs("payments")["url"] == kwargs["url"]
    preview_payload = {"issue_id": "28ad5060-28e6-4ba5-ac6a-9990691c11c3", "rule": rule}
    preview = PreviewIssueCompoundAlertRequest.from_dict(preview_payload)
    kwargs = preview_issue_compound_alert._get_kwargs("payments", body=preview)
    assert kwargs["method"] == "post"
    assert kwargs["url"].endswith("/issue-compound-alert-policy/preview")
    assert kwargs["json"] == preview_payload
