from faas_sdk.models.workflow_recovery_automatic_blocked_notification import (
    WorkflowRecoveryAutomaticBlockedNotification,
)
from faas_sdk.models.workflow_recovery_queue_blocked_webhook_payload import WorkflowRecoveryQueueBlockedWebhookPayload


def test_retry_evidence_alert_guidance_round_trip():
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    body = {
        "event_id": ident,
        "app_id": ident,
        "app": "orders",
        "review_id": ident,
        "action_id": ident,
        "policy_version": 1,
        "position": 3,
        "status": "pending",
        "reason": "terminal_outcome_not_saved",
        "observed_at": stamp,
        "queue_url": "/queue",
        "review_status_url": "/review/status",
        "state": "blocked",
        "evidence_kind": "parent",
        "run_id": ident,
        "resume_count": 1,
        "diagnosis_reason": "terminal_outcome_not_saved",
        "repairable": True,
        "evidence_url": "/review/actions/action/evidence/parent",
        "cli_command": "gregale automations recovery-retry-evidence --app orders --parent",
    }
    assert WorkflowRecoveryQueueBlockedWebhookPayload.from_dict(body).to_dict() == body
    receipt = {
        "event_id": ident,
        "status": "queued",
        "reason": "terminal_outcome_not_saved",
        "captured_at": stamp,
        "receiver_webhook_ids": [ident],
        "state": "blocked",
        "sequence": 1,
        "evidence_kind": "parent",
        "diagnosis_reason": "terminal_outcome_not_saved",
    }
    assert WorkflowRecoveryAutomaticBlockedNotification.from_dict(receipt).to_dict() == receipt
    for key in ("state", "sequence", "evidence_kind", "diagnosis_reason"):
        del receipt[key]
    assert WorkflowRecoveryAutomaticBlockedNotification.from_dict(receipt).to_dict() == receipt
    body.update(state="recovered", reason="blocker_cleared", repairable=False)
    assert WorkflowRecoveryQueueBlockedWebhookPayload.from_dict(body).to_dict() == body
