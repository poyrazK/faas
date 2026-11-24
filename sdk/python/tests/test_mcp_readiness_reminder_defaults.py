from uuid import UUID


def test_reminder_defaults_wire_contract():
    from faas_sdk.preview.api.mcp import (
        get_mcp_readiness_reminder_defaults,
        set_mcp_readiness_reminder_defaults,
    )
    from faas_sdk.preview.models.mcp_readiness_incident_attention import MCPReadinessIncidentAttention
    from faas_sdk.preview.models.mcp_readiness_reminder_defaults import MCPReadinessReminderDefaults
    from faas_sdk.preview.models.set_mcp_readiness_reminder_defaults_request import SetMCPReadinessReminderDefaultsRequest

    rule = UUID("00000000-0000-0000-0000-000000000001")
    off = dict(interval_seconds=0, max_reminders=0)
    request = dict(expected_version=0, reminder_policy=off)
    body = SetMCPReadinessReminderDefaultsRequest.from_dict(request)
    assert body.to_dict() == request
    get = get_mcp_readiness_reminder_defaults._get_kwargs("demo", rule)
    put = set_mcp_readiness_reminder_defaults._get_kwargs("demo", rule, body=body)
    assert get["url"] == f"/v1/apps/demo/alerts/{rule}/mcp-readiness-reminder-defaults"
    assert put["method"] == "put"
    assert put["url"] == get["url"]
    assert put["json"] == request
    assert MCPReadinessReminderDefaults.from_dict(dict(version=0, reminder_policy=off)).to_dict() == dict(
        version=0, reminder_policy=off
    )
    attention = dict(
        incident_id=str(rule),
        rule_id=str(rule),
        rule_name="Readiness",
        rule_enabled=False,
        opened_at="2026-10-17T12:00:00+00:00",
        version=0,
        state="unacknowledged",
        reminder_policy=off,
        reminders_created=0,
        reminder_policy_source="rule_default",
        reminder_defaults_version=0,
    )
    assert MCPReadinessIncidentAttention.from_dict(attention).to_dict() == attention
    attention["reminder_policy_source"] = "incident_override"
    assert MCPReadinessIncidentAttention.from_dict(attention).to_dict()["reminder_defaults_version"] == 0
    del attention["reminder_policy_source"]
    del attention["reminder_defaults_version"]
    assert "reminder_defaults_version" not in MCPReadinessIncidentAttention.from_dict(attention).to_dict()
