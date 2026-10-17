from typing import get_args

from faas_sdk.api.apps import get_issue_frequency_alert_policy, set_issue_frequency_alert_policy
from faas_sdk.models.create_app_webhook_request_event_filter_item import CreateAppWebhookRequestEventFilterItem
from faas_sdk.models.issue_frequency_alert_policy import IssueFrequencyAlertPolicy
from faas_sdk.models.update_issue_frequency_alert_policy_request import UpdateIssueFrequencyAlertPolicyRequest


def test_frequency_policy_retains_revision_environment_window_and_cooldown():
    rule = {
        "key": "burst",
        "enabled": True,
        "environment": "production",
        "minimum_events": 50,
        "window_seconds": 300,
        "cooldown_seconds": 1800,
    }
    body = UpdateIssueFrequencyAlertPolicyRequest.from_dict({"expected_revision": 7, "rules": [rule]})
    kwargs = set_issue_frequency_alert_policy._get_kwargs("payments", body=body)
    assert kwargs["method"] == "put"
    assert kwargs["url"] == "/v1/apps/payments/issue-frequency-alert-policy"
    assert kwargs["json"] == {"expected_revision": 7, "rules": [rule]}
    policy = IssueFrequencyAlertPolicy.from_dict({"revision": 8, "rules": [rule]})
    assert policy.to_dict() == {"revision": 8, "rules": [rule]}
    assert get_issue_frequency_alert_policy._get_kwargs("payments")["url"] == kwargs["url"]
    assert "issue.frequency_threshold_reached" in get_args(CreateAppWebhookRequestEventFilterItem)
