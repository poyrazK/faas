from uuid import UUID


def test_pagerduty_config_and_provider_contracts():
    from faas_sdk.preview.api.mcp import get_mcp_readiness_pager_duty, set_mcp_readiness_pager_duty
    from faas_sdk.preview.models.mcp_readiness_destination_delivery import MCPReadinessDestinationDelivery
    from faas_sdk.preview.models.mcp_readiness_pager_duty_policy import MCPReadinessPagerDutyPolicy
    from faas_sdk.preview.models.set_mcp_readiness_pager_duty_request import SetMCPReadinessPagerDutyRequest

    rule = UUID("00000000-0000-0000-0000-000000000001")
    request = dict(expected_version=0, enabled=True, integration_key="a" * 32, region="eu")
    body = SetMCPReadinessPagerDutyRequest.from_dict(request)
    assert body.to_dict() == request
    get = get_mcp_readiness_pager_duty._get_kwargs("demo", rule)
    put = set_mcp_readiness_pager_duty._get_kwargs("demo", rule, body=body)
    assert get["url"] == f"/v1/apps/demo/alerts/{rule}/mcp-readiness-pagerduty"
    assert put["method"] == "put"
    assert put["url"] == get["url"]
    assert put["json"] == request
    disabled = dict(expected_version=1, enabled=False)
    assert SetMCPReadinessPagerDutyRequest.from_dict(disabled).to_dict() == disabled
    masked = MCPReadinessPagerDutyPolicy.from_dict(
        dict(version=1, enabled=True, region="eu", integration_key_masked="***")
    ).to_dict()
    assert "integration_key" not in masked
    assert masked["region"] == "eu"
    delivery = dict(
        destination="primary", status="delivered", attempt_count=1, provider="pagerduty", provider_status="accepted"
    )
    assert MCPReadinessDestinationDelivery.from_dict(delivery).to_dict() == delivery
