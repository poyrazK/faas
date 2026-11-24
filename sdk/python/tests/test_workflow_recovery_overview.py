import pytest

from faas_sdk.preview.models.workflow_recovery_overview import WorkflowRecoveryOverview


@pytest.mark.parametrize("delivery", ["no_receivers", "relay_pending", "pending", "delivered", "failed", "unknown"])
def test_recovery_overview_delivery_round_trip(delivery):
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    receivers = [] if delivery == "no_receivers" else [ident]
    payload = {
        "event_id": ident,
        "app_id": ident,
        "app": "orders",
        "scope": "manual_initial",
        "review_id": ident,
        "policy_version": 2,
        "sequence": 1,
        "state": "failed",
        "code": "invalid_recovery_identity",
        "attempts": 3,
        "observed_at": stamp,
        "inspection_url": "/v1/apps/orders/initial-recovery-reviews/" + ident,
        "monitoring_only": True,
    }
    body = {
        "status": "needs_attention",
        "monitoring_status": "failing",
        "observed_at": stamp,
        "monitoring_only": True,
        "manual_awaiting_approval": 0,
        "manual_unresolved": 1,
        "manual_polling_failures": 1,
        "manual_attention": 0,
        "truncated": False,
        "issues": [
            {
                "scope": "manual_initial",
                "reason": "invalid_recovery_identity",
                "policy_version": 2,
                "review_id": ident,
                "retry_at": stamp,
                "inspection_url": payload["inspection_url"],
            }
        ],
        "last_alert": {
            "notification": {
                "payload": payload,
                "status": "queued" if receivers else "no_receivers",
                "captured_at": stamp,
                "receiver_webhook_ids": receivers,
            },
            "delivery_status": delivery,
            "expected": len(receivers),
            "pending": 0,
            "in_flight": 0,
            "succeeded": int(delivery == "delivered"),
            "failed": 0,
            "dead": int(delivery == "failed"),
            "missing": int(delivery in ["unknown", "relay_pending"]),
            "relay_pending": delivery == "relay_pending",
        },
    }
    parsed = WorkflowRecoveryOverview.from_dict(body)
    assert parsed.to_dict() == body
    assert parsed.monitoring_status == "failing"
    assert parsed.last_alert.delivery_status == delivery
