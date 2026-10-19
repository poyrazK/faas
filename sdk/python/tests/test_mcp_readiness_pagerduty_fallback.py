from uuid import UUID


def test_native_fallback_and_acknowledgement_contracts():
    from faas_sdk.api.mcp import (
        get_mcp_readiness_fallback,
        set_mcp_readiness_fallback,
        set_mcp_readiness_pager_duty_acknowledgements,
    )
    from faas_sdk.models.mcp_readiness_fallback_policy import MCPReadinessFallbackPolicy
    from faas_sdk.models.mcp_readiness_pager_duty_acknowledgement_events import (
        MCPReadinessPagerDutyAcknowledgementEvents,
    )
    from faas_sdk.models.mcp_readiness_pager_duty_acknowledgement_policy import (
        MCPReadinessPagerDutyAcknowledgementPolicy,
    )
    from faas_sdk.models.set_mcp_readiness_fallback_request import SetMCPReadinessFallbackRequest
    from faas_sdk.models.set_mcp_readiness_pager_duty_acknowledgements_request import (
        SetMCPReadinessPagerDutyAcknowledgementsRequest,
    )

    rule = UUID("00000000-0000-0000-0000-000000000001")
    request = dict(
        expected_version=0, enabled=True, provider="pagerduty", region="eu", integration_key="a" * 32, after_failures=2
    )
    body = SetMCPReadinessFallbackRequest.from_dict(request)
    assert body.to_dict() == request
    kwargs = set_mcp_readiness_fallback._get_kwargs("demo", rule, body=body)
    assert kwargs["url"] == f"/v1/apps/demo/alerts/{rule}/mcp-readiness-fallback"
    assert kwargs["json"] == request and kwargs["method"] == "put"
    assert get_mcp_readiness_fallback._get_kwargs("demo", rule)["url"] == kwargs["url"]
    disabled = dict(expected_version=1, enabled=False)
    assert SetMCPReadinessFallbackRequest.from_dict(disabled).to_dict() == disabled
    policy = dict(
        version=1, enabled=True, provider="pagerduty", region="eu", integration_key_masked="***", after_failures=2
    )
    assert MCPReadinessFallbackPolicy.from_dict(policy).to_dict() == policy
    binding = dict(
        expected_version=0,
        enabled=True,
        destination="fallback",
        expected_fallback_version=1,
        service_id="PSVC001",
        signing_secret="s" * 32,
    )
    bound_request = SetMCPReadinessPagerDutyAcknowledgementsRequest.from_dict(binding)
    assert bound_request.to_dict() == binding
    bind_kwargs = set_mcp_readiness_pager_duty_acknowledgements._get_kwargs("demo", rule, body=bound_request)
    assert bind_kwargs["url"] == f"/v1/apps/demo/alerts/{rule}/mcp-readiness-pagerduty-acknowledgements"
    assert bind_kwargs["json"] == binding
    assert "expected_pagerduty_version" not in bind_kwargs["json"]
    bound = dict(
        version=1,
        enabled=True,
        destination="fallback",
        fallback_version=1,
        region="eu",
        service_id="PSVC001",
        webhook_path=f"/v1/webhooks/mcp-readiness/pagerduty/{rule}",
    )
    assert MCPReadinessPagerDutyAcknowledgementPolicy.from_dict(bound).to_dict() == bound
    event = dict(
        id=str(rule),
        event_id="fallback-event",
        incident_id=str(rule),
        provider_incident_id="PINC001",
        occurred_at="2026-10-17T00:00:00+00:00",
        received_at="2026-10-17T00:00:01+00:00",
        policy_version=1,
        outcome="acknowledged",
        attention_version=1,
        region="eu",
        service_id="PSVC001",
        destination="fallback",
    )
    assert MCPReadinessPagerDutyAcknowledgementEvents.from_dict(dict(events=[event])).to_dict() == dict(events=[event])
