from faas_sdk.models.mcp_readiness_notification_action import MCPReadinessNotificationAction
from faas_sdk.models.mcp_readiness_notification_queues import MCPReadinessNotificationQueues
from faas_sdk.models.mcp_readiness_notification_receipt import MCPReadinessNotificationReceipt


def test_notification_recovery_contract_round_trip():
    event_id = "00000000-0000-0000-0000-000000000001"
    request_id = "00000000-0000-0000-0000-000000000002"
    actor = "00000000-0000-0000-0000-000000000003"
    at = "2026-10-16T00:00:00Z"
    action = dict(request_id=request_id, event_id=event_id, action="abandon", expected_version=0, reason="Retired")
    assert MCPReadinessNotificationAction.from_dict(action).to_dict() == action
    receipt = dict(
        request_id=request_id,
        event_id=event_id,
        action="abandon",
        reason="Retired",
        created_at=at,
        created_by=actor,
        delivery_version=1,
    )
    assert MCPReadinessNotificationReceipt.from_dict(receipt).to_dict()["delivery_version"] == 1
    queue = dict(
        rule_id=actor,
        name="Readiness",
        enabled=True,
        outstanding_count=2,
        blocked_count=1,
        oldest_age_seconds=60.0,
        state="delayed",
        failure_kind="http_5xx",
        can_retry=True,
        can_abandon=True,
        head=dict(
            id=event_id,
            event_id=event_id,
            incident_id=actor,
            rule_id=actor,
            event="alert.fired",
            scopes=["staging"],
            occurred_at=at,
            incident_status="open",
            delivery_status="abandoned",
            delivery_version=1,
            attempt_count=1,
            subjects=[],
            last_operator_action=receipt,
        ),
    )
    page = MCPReadinessNotificationQueues.from_dict(dict(observed_at=at, queues=[queue])).to_dict()
    head = page["queues"][0]["head"]
    assert head["delivery_status"] == "abandoned"
    assert head["last_operator_action"]["reason"] == "Retired"
    assert "next_attempt_at" not in head
