import pytest

from faas_sdk.models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification


@pytest.mark.parametrize("state", ["failed", "recovered"])
@pytest.mark.parametrize("scope", ["manual_initial", "automatic_policy"])
def test_monitoring_alert_receipt_round_trip(state, scope):
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T20:00:00+00:00"
    payload = {
        "event_id": ident,
        "app_id": ident,
        "app": "orders",
        "scope": scope,
        "policy_version": 2,
        "sequence": 1 if state == "failed" else 2,
        "state": state,
        "code": "invalid_recovery_identity",
        "attempts": 3,
        "observed_at": stamp,
        "inspection_url": "/v1/apps/orders/recovery-policy",
        "monitoring_only": True,
    }
    if scope == "manual_initial":
        payload["review_id"] = ident
    if state == "failed":
        payload["retry_at"] = "2026-10-13T20:04:00+00:00"
    body = {"payload": payload, "status": "queued", "captured_at": stamp, "receiver_webhook_ids": [ident]}
    parsed = WorkflowRecoveryPollingNotification.from_dict(body)
    assert parsed.to_dict() == body
    assert parsed.payload.monitoring_only
    assert "result" not in parsed.payload.to_dict()
