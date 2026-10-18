from uuid import UUID


def test_pagerduty_acknowledgement_contracts():
    from faas_sdk.api.mcp import (
        get_mcp_readiness_pager_duty_acknowledgements,
        list_mcp_readiness_pager_duty_acknowledgement_events,
        set_mcp_readiness_pager_duty_acknowledgements,
    )
    from faas_sdk.models.mcp_readiness_acknowledgement import MCPReadinessAcknowledgement
    from faas_sdk.models.mcp_readiness_pager_duty_acknowledgement_events import (
        MCPReadinessPagerDutyAcknowledgementEvents,
    )
    from faas_sdk.models.mcp_readiness_pager_duty_acknowledgement_policy import (
        MCPReadinessPagerDutyAcknowledgementPolicy,
    )
    from faas_sdk.models.set_mcp_readiness_pager_duty_acknowledgements_request import (
        SetMCPReadinessPagerDutyAcknowledgementsRequest,
    )

    rule = UUID("00000000-0000-0000-0000-000000000001")
    path = f"/v1/apps/demo/alerts/{rule}/mcp-readiness-pagerduty-acknowledgements"
    request = dict(
        expected_version=0,
        enabled=True,
        sync_unacknowledgements=True,
        expected_pagerduty_version=1,
        service_id="PSVC001",
        signing_secret="s" * 32,
    )
    body = SetMCPReadinessPagerDutyAcknowledgementsRequest.from_dict(request)
    assert body.to_dict() == request
    assert get_mcp_readiness_pager_duty_acknowledgements._get_kwargs("demo", rule)["url"] == path
    selected = get_mcp_readiness_pager_duty_acknowledgements._get_kwargs("demo", rule, destination="fallback")
    assert selected["params"] == {"destination": "fallback"}
    fallback_disabled = dict(expected_version=2, enabled=False, destination="fallback")
    assert SetMCPReadinessPagerDutyAcknowledgementsRequest.from_dict(fallback_disabled).to_dict() == fallback_disabled
    put = set_mcp_readiness_pager_duty_acknowledgements._get_kwargs("demo", rule, body=body)
    assert put["json"] == request and put["method"] == "put"
    disabled = dict(expected_version=1, enabled=False)
    assert SetMCPReadinessPagerDutyAcknowledgementsRequest.from_dict(disabled).to_dict() == disabled
    policy = dict(
        version=1,
        enabled=True,
        sync_unacknowledgements=True,
        region="eu",
        service_id="PSVC001",
        pagerduty_version=1,
        signing_secret_masked="***",
        webhook_path=f"/v1/webhooks/mcp-readiness/pagerduty/{rule}",
    )
    assert MCPReadinessPagerDutyAcknowledgementPolicy.from_dict(policy).to_dict() == policy
    kwargs = list_mcp_readiness_pager_duty_acknowledgement_events._get_kwargs("demo", rule, before="cursor", limit=1)
    assert kwargs["url"] == path + "/events"
    assert kwargs["params"] == dict(before="cursor", limit=1)
    event = dict(
        id=str(rule),
        event_id="provider-1",
        incident_id=str(rule),
        provider_incident_id="PINC001",
        occurred_at="2026-10-17T00:00:00+00:00",
        received_at="2026-10-17T00:00:01+00:00",
        policy_version=1,
        attention_version=1,
        outcome="released",
        action="release",
        destination="fallback",
        region="eu",
        service_id="PSVC001",
    )
    assert MCPReadinessPagerDutyAcknowledgementEvents.from_dict(dict(events=[event])).to_dict() == dict(events=[event])
    acknowledgement = dict(
        request_id=str(rule),
        created_by=str(rule),
        created_at=event["received_at"],
        reason="Acknowledged in PagerDuty",
        source="pagerduty",
        provider_event_id=event["event_id"],
        provider_incident_id="PINC001",
        provider_occurred_at=event["occurred_at"],
    )
    assert MCPReadinessAcknowledgement.from_dict(acknowledgement).to_dict() == acknowledgement


def test_pagerduty_hold_contract():
    from faas_sdk.models.mcp_readiness_incident_attention import MCPReadinessIncidentAttention
    from faas_sdk.models.mcp_readiness_pager_duty_acknowledgement_hold import MCPReadinessPagerDutyAcknowledgementHold

    hold = dict(
        destination="primary",
        provider_incident_id="PINC001",
        provider_event_id="event",
        service_id="PSVC001",
        region="eu",
        occurred_at="2026-10-17T00:00:00+00:00",
        received_at="2026-10-17T00:00:01+00:00",
        policy_version=1,
        release_sync_enabled=True,
    )
    assert MCPReadinessPagerDutyAcknowledgementHold.from_dict(hold).to_dict() == hold
    attention = dict(
        incident_id="00000000-0000-0000-0000-000000000001",
        rule_id="00000000-0000-0000-0000-000000000002",
        rule_name="readiness",
        rule_enabled=True,
        opened_at="2026-10-17T00:00:00+00:00",
        version=2,
        state="acknowledged",
        reminder_policy=dict(interval_seconds=60, max_reminders=2),
        reminders_created=0,
        provider_acknowledgements=[hold],
    )
    assert MCPReadinessIncidentAttention.from_dict(attention).to_dict() == attention
