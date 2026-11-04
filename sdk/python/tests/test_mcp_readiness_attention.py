from faas_sdk.preview.models.mcp_readiness_attention_action import MCPReadinessAttentionAction
from faas_sdk.preview.models.mcp_readiness_attention_history import MCPReadinessAttentionHistory
from faas_sdk.preview.models.mcp_readiness_attention_page import MCPReadinessAttentionPage
from faas_sdk.preview.models.mcp_readiness_incident_event import MCPReadinessIncidentEvent


def test_incident_attention_round_trip():
    incident = "00000000-0000-0000-0000-000000000001"
    request = "00000000-0000-0000-0000-000000000002"
    actor = "00000000-0000-0000-0000-000000000003"
    at = "2026-10-17T12:00:00Z"
    policy = dict(interval_seconds=60, max_reminders=3)
    action = dict(
        request_id=request,
        expected_version=0,
        action="configure_reminders",
        reason="Investigating",
        reminder_policy=policy,
    )
    assert MCPReadinessAttentionAction.from_dict(action).to_dict() == action
    receipt = dict(
        request_id=request,
        incident_id=incident,
        action="configure_reminders",
        reason="Investigating",
        created_by=actor,
        created_at=at,
        version=1,
        reminder_policy=policy,
    )
    attention = dict(
        incident_id=incident,
        rule_id=actor,
        rule_name="Readiness",
        rule_enabled=True,
        opened_at=at,
        version=1,
        state="acknowledged",
        reminder_policy=policy,
        reminders_created=1,
        acknowledgement=dict(request_id=request, created_by=actor, created_at=at, reason="Investigating"),
        last_action=receipt,
    )
    page = MCPReadinessAttentionPage.from_dict(dict(incidents=[attention])).to_dict()
    assert page["incidents"][0]["acknowledgement"]["reason"] == "Investigating"
    assert "expires_at" not in page["incidents"][0]["acknowledgement"]
    assert "next_reminder_at" not in page["incidents"][0]
    history = MCPReadinessAttentionHistory.from_dict(dict(actions=[receipt])).to_dict()
    assert history["actions"][0]["reminder_policy"] == policy
    event = dict(
        id=request,
        incident_id=incident,
        event_id=request,
        event="alert.fired",
        rule_id=actor,
        scopes=["staging"],
        occurred_at=at,
        incident_status="open",
        delivery_status="suppressed",
        attempt_count=0,
        subjects=[],
        notification_reason="reminder",
        reminder_number=1,
        suppression_reason="acknowledged",
    )
    parsed = MCPReadinessIncidentEvent.from_dict(event).to_dict()
    assert parsed["notification_reason"] == "reminder"
    assert parsed["reminder_number"] == 1
    assert parsed["suppression_reason"] == "acknowledged"


def test_incident_attention_request_paths():
    from uuid import UUID

    from faas_sdk.preview.api.mcp import act_mcp_readiness_attention, list_mcp_readiness_attention

    incident = UUID("00000000-0000-0000-0000-000000000001")
    body = MCPReadinessAttentionAction.from_dict(
        dict(request_id=str(incident), expected_version=0, action="release", reason="Handoff")
    )
    post = act_mcp_readiness_attention._get_kwargs("demo", incident, body=body)
    assert post["url"] == f"/v1/apps/demo/mcp-readiness-incidents/{incident}/attention-actions"
    assert post["json"]["expected_version"] == 0
    query = list_mcp_readiness_attention._get_kwargs("demo", scope="staging", include_resolved=True)
    assert query["params"]["include_resolved"] is True
