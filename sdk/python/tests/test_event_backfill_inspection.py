from faas_sdk.models.event_receipt_response import EventReceiptResponse
from faas_sdk.models.event_replay_backfill_item_response import EventReplayBackfillItemResponse


def test_receipt_preserves_acceptance_counts_and_backfill_handler_outcome():
    wire = {
        "event_id": "order-1",
        "event_source": "orders",
        "event_type": "created",
        "accepted_at": "2026-10-07T12:00:00+00:00",
        "snapshot_captured": True,
        "routing_mode": "recipient",
        "recipient_count": 0,
        "routing_summary": {},
        "backfill_recipient_count": 1,
        "backfill_routing_summary": {"enqueued": 1},
        "recipients": [
            {
                "subscription_id": "added-consumer",
                "app_id": "11111111-1111-4111-8111-111111111111",
                "origin": "backfill",
                "backfill_job_id": "22222222-2222-4222-8222-222222222222",
                "backfill_job_url": "/v1/event-replays/22222222-2222-4222-8222-222222222222",
                "routing": {"state": "enqueued", "attempts": 1, "retryable": False, "replay_count": 0},
                "execution": {
                    "invocation_id": "33333333-3333-4333-8333-333333333333",
                    "state": "pending",
                    "attempts": 0,
                    "replay_generation": 0,
                    "created_at": "2026-10-07T12:01:00+00:00",
                },
                "recovery_actions": [],
            }
        ],
    }
    receipt = EventReceiptResponse.from_dict(wire)
    assert receipt.recipient_count == 0
    assert receipt.recipients[0].execution.state == "pending"
    assert receipt.to_dict() == wire


def test_expired_backfill_item_omits_live_inspection_links():
    wire = {
        "event_source": "orders",
        "event_id": "order-1",
        "event_type": "created",
        "accepted_at": "2026-10-07T12:00:00+00:00",
        "state": "enqueued",
        "attempts": 1,
        "retryable": False,
        "updated_at": "2026-10-07T12:01:00+00:00",
    }
    assert EventReplayBackfillItemResponse.from_dict(wire).to_dict() == wire
    links = {
        "receipt_url": "/v1/events/receipt?source=orders&id=order-1",
        "attempt_history_url": "/v1/events/receipt/attempts?source=orders&id=order-1&subscription_id=added",
    }
    retained = {**wire, **links}
    assert EventReplayBackfillItemResponse.from_dict(retained).to_dict() == retained
