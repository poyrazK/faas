import json

import httpx

from faas_sdk.api.alert_rules import enable_alert_preset
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import EnableAlertPresetRequest


def test_automation_backlog_preset_decodes_due_age_and_keeps_notification_defaults():
    rule = {
        "id": "0123456789abcdef0123456789abcdef",
        "app_id": "1123456789abcdef0123456789abcdef",
        "name": "Automation backlog exceeds five minutes",
        "enabled": True,
        "metric": "workflow_due_age_seconds",
        "comparison": "gte",
        "threshold": 300,
        "window_spec": "5m",
        "webhook_url": "https://example.com/hook",
        "webhook_secret_sealed_masked": "***",
        "cooldown_minutes": 30,
        "action": "webhook",
        "state": "ok",
        "created_at": "2026-10-07T12:00:00+00:00",
        "updated_at": "2026-10-07T12:00:00+00:00",
    }

    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/apps/billing/alert-presets/automation_backlog/enable"
        body = json.loads(request.content)
        assert body["webhook_url"] == rule["webhook_url"]
        assert body["webhook_secret"] == "test-secret"
        assert body.get("action", "webhook") == "webhook" and "cooldown_minutes" not in body
        return httpx.Response(201, json=rule)

    with AuthenticatedClient(
        base_url="https://example.test", token="test-token", httpx_args={"transport": httpx.MockTransport(respond)}
    ) as client:
        result = enable_alert_preset.sync(
            "billing",
            "automation_backlog",
            client=client,
            body=EnableAlertPresetRequest(webhook_url=rule["webhook_url"], webhook_secret="test-secret"),
        )

    assert result is not None and result.metric == "workflow_due_age_seconds"
    assert result.to_dict() == rule
