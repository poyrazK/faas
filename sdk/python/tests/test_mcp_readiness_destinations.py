from uuid import UUID


def test_destination_queue_models_and_targeted_request():
    from faas_sdk.preview.api.mcp import act_mcp_readiness_notification
    from faas_sdk.preview.models.mcp_readiness_notification_action import MCPReadinessNotificationAction
    from faas_sdk.preview.models.mcp_readiness_notification_queues import MCPReadinessNotificationQueues
    from faas_sdk.preview.models.mcp_readiness_notification_receipt import MCPReadinessNotificationReceipt

    event_id = "00000000-0000-0000-0000-000000000001"
    at = "2026-10-17T12:00:00Z"
    request = dict(
        request_id=event_id,
        event_id=event_id,
        action="retry",
        expected_version=0,
        reason="Receiver repaired",
        destination="primary",
    )
    body = MCPReadinessNotificationAction.from_dict(request)
    kwargs = act_mcp_readiness_notification._get_kwargs("demo", body=body)
    assert kwargs["url"] == "/v1/apps/demo/mcp-readiness-notifications/actions"
    assert kwargs["json"] == request
    receipt = MCPReadinessNotificationReceipt.from_dict(
        dict(
            request_id=event_id,
            event_id=event_id,
            action="retry",
            reason="Receiver repaired",
            destination="primary",
            created_at=at,
            created_by=event_id,
            delivery_version=1,
        )
    )
    assert receipt.to_dict()["destination"] == "primary"
    assert receipt.event_id == UUID(event_id)
    head = dict(
        id=event_id,
        event_id=event_id,
        incident_id=event_id,
        rule_id=event_id,
        event="alert.resolved",
        scopes=["staging"],
        occurred_at=at,
        incident_status="resolved",
        delivery_status="failed",
        delivery_version=0,
        attempt_count=1,
        subjects=[],
        destination_deliveries=[
            dict(destination="primary", status="failed", attempt_count=1, next_attempt_at=at),
            dict(destination="fallback", status="delivered", attempt_count=1),
        ],
    )
    primary = dict(
        destination="primary",
        outstanding_count=1,
        blocked_count=0,
        state="delayed",
        can_retry=True,
        can_abandon=True,
        head=head,
    )
    queue = dict(
        rule_id=event_id,
        name="Readiness",
        enabled=True,
        outstanding_count=1,
        blocked_count=0,
        oldest_age_seconds=60,
        state="delayed",
        can_retry=True,
        can_abandon=True,
        destinations=[primary],
    )
    page = MCPReadinessNotificationQueues.from_dict(dict(observed_at=at, queues=[queue])).to_dict()
    deliveries = page["queues"][0]["destinations"][0]["head"]["destination_deliveries"]
    assert deliveries[0]["next_attempt_at"].startswith("2026-10-17T12:00:00")
    assert deliveries[1] == dict(destination="fallback", status="delivered", attempt_count=1)
