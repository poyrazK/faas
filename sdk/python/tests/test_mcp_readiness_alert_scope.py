from faas_sdk.preview.models.create_alert_rule_request import CreateAlertRuleRequest
from faas_sdk.preview.models.update_alert_rule_request import UpdateAlertRuleRequest


def test_readiness_alert_scope_round_trip():
    for scope in ("", "default", "staging"):
        body = dict(
            name="Readiness",
            metric="mcp_readiness_unhealthy",
            comparison="gt",
            threshold=0,
            window_spec="5m",
            webhook_url="https://example.com/hook",
            webhook_secret="fixture",
            mcp_readiness_scope=scope,
        )
        model = CreateAlertRuleRequest.from_dict(body)
        assert model.to_dict()["mcp_readiness_scope"] == scope
        patch = UpdateAlertRuleRequest.from_dict(dict(mcp_readiness_scope=scope))
        assert patch.to_dict()["mcp_readiness_scope"] == scope
    legacy = dict(
        name="Readiness",
        metric="mcp_readiness_unhealthy",
        comparison="gt",
        threshold=0,
        window_spec="5m",
        webhook_url="https://example.com/hook",
        webhook_secret="fixture",
    )
    assert "mcp_readiness_scope" not in CreateAlertRuleRequest.from_dict(legacy).to_dict()
