from faas_sdk.preview.models.mcp_readiness_incident_timeline import MCPReadinessIncidentTimeline


def test_readiness_incident_event_round_trip():
    event = dict(
        id="00000000-0000-0000-0000-000000000001",
        incident_id="00000000-0000-0000-0000-000000000002",
        event_id="00000000-0000-0000-0000-000000000003",
        event="alert.resolved",
        rule_id="00000000-0000-0000-0000-000000000004",
        mcp_readiness_scope="staging",
        scopes=["staging"],
        occurred_at="2026-10-16T00:00:00Z",
        attempted_at="2026-10-16T00:05:00Z",
        delivery_status="failed",
        incident_status="resolved",
        attempt_count=2,
        subjects=[],
    )
    page = MCPReadinessIncidentTimeline.from_dict(dict(events=[event], next_before="cursor"))
    loaded = page.to_dict()["events"][0]
    assert loaded["incident_id"] == event["incident_id"]
    assert loaded["event_id"] == event["event_id"]
    assert loaded["event"] == "alert.resolved"
    assert loaded["scopes"] == ["staging"]
    assert page.to_dict()["next_before"] == "cursor"

    assert loaded["incident_status"] == "resolved"
    assert loaded["delivery_status"] == "failed"
    assert loaded["attempt_count"] == 2
    pending = dict(event, delivery_status="pending", attempt_count=0)
    pending.pop("attempted_at")
    empty = MCPReadinessIncidentTimeline.from_dict(dict(events=[pending]))
    assert "attempted_at" not in empty.to_dict()["events"][0]


def test_suppressed_readiness_event_round_trip():
    event = dict(
        id="00000000-0000-0000-0000-000000000001",
        incident_id="00000000-0000-0000-0000-000000000002",
        event_id="00000000-0000-0000-0000-000000000001",
        event="alert.fired",
        rule_id="00000000-0000-0000-0000-000000000004",
        scopes=["staging"],
        occurred_at="2026-10-16T00:00:00Z",
        incident_status="open",
        delivery_status="suppressed",
        attempt_count=0,
        subjects=[],
        suppression_reason="maintenance",
        maintenance_window_ids=["00000000-0000-0000-0000-000000000005"],
    )
    loaded = MCPReadinessIncidentTimeline.from_dict(dict(events=[event])).to_dict()["events"][0]
    assert loaded["delivery_status"] == "suppressed"
    assert loaded["suppression_reason"] == "maintenance"
    assert loaded["maintenance_window_ids"] == event["maintenance_window_ids"]
    assert "next_attempt_at" not in loaded
