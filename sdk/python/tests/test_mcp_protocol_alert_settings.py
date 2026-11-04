from faas_sdk.preview.models.alert_rule_response import AlertRuleResponse
from faas_sdk.preview.models.create_alert_rule_request import CreateAlertRuleRequest
from faas_sdk.preview.models.update_alert_rule_request import UpdateAlertRuleRequest


def test_mcp_alert_settings_round_trip():
    for metric in ("mcp_protocol_error_rate_pct", "mcp_protocol_execution_p95_ms"):
        body = dict(
            name="MCP performance",
            metric=metric,
            comparison="gt",
            threshold=10,
            window_spec="5m",
            webhook_url="https://example.com/hook",
            webhook_secret="test-secret",
            mcp_min_calls=40,
            mcp_for_minutes=3,
        )
        created = CreateAlertRuleRequest.from_dict(body)
        assert created.to_dict()["mcp_min_calls"] == 40
        assert created.to_dict()["mcp_for_minutes"] == 3
        patch = UpdateAlertRuleRequest.from_dict(dict(mcp_min_calls=80, mcp_for_minutes=5))
        assert patch.to_dict() == dict(mcp_min_calls=80, mcp_for_minutes=5)
        response = dict(
            id="rule",
            app_id="app",
            name="MCP performance",
            enabled=True,
            metric=metric,
            comparison="gt",
            threshold=10,
            window_spec="5m",
            webhook_url="https://example.com/hook",
            webhook_secret_sealed_masked="***",
            cooldown_minutes=15,
            action="webhook",
            state="unknown",
            created_at="2026-10-15T00:00:00Z",
            updated_at="2026-10-15T00:00:00Z",
            mcp_min_calls=40,
            mcp_for_minutes=3,
        )
        loaded = AlertRuleResponse.from_dict(response)
        assert loaded.metric == metric
        assert loaded.to_dict()["mcp_min_calls"] == 40
