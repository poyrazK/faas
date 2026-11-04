from uuid import UUID


def test_fallback_models_and_request_paths():
    from faas_sdk.preview.api.mcp import get_mcp_readiness_fallback, set_mcp_readiness_fallback
    from faas_sdk.preview.models.mcp_readiness_delivery_routing import MCPReadinessDeliveryRouting
    from faas_sdk.preview.models.mcp_readiness_fallback_policy import MCPReadinessFallbackPolicy
    from faas_sdk.preview.models.set_mcp_readiness_fallback_request import SetMCPReadinessFallbackRequest

    rule = UUID("00000000-0000-0000-0000-000000000001")
    request = dict(
        expected_version=0,
        enabled=True,
        webhook_url="https://fallback.example.com/hook",
        webhook_secret="private-key",
        after_failures=2,
    )
    body = SetMCPReadinessFallbackRequest.from_dict(request)
    assert body.to_dict() == request
    get = get_mcp_readiness_fallback._get_kwargs("demo", rule)
    put = set_mcp_readiness_fallback._get_kwargs("demo", rule, body=body)
    assert get["url"] == f"/v1/apps/demo/alerts/{rule}/mcp-readiness-fallback"
    assert put["url"] == get["url"]
    assert put["method"] == "put"
    assert put["json"] == request
    disabled = SetMCPReadinessFallbackRequest.from_dict(dict(expected_version=1, enabled=False))
    assert disabled.to_dict() == dict(expected_version=1, enabled=False)
    policy = MCPReadinessFallbackPolicy.from_dict(
        dict(
            version=1,
            enabled=True,
            webhook_url=request["webhook_url"],
            webhook_secret_sealed_masked="***",
            after_failures=2,
        )
    ).to_dict()
    assert "webhook_secret" not in policy
    progress = dict(
        policy_version=1,
        last_destination="fallback",
        failover_reason="primary_failure_threshold",
        primary_attempts=2,
        fallback_attempts=1,
        primary_failures=2,
        primary_delivered=False,
        fallback_delivered=True,
    )
    assert MCPReadinessDeliveryRouting.from_dict(progress).to_dict() == progress
