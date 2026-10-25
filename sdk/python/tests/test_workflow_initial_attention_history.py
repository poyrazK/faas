from faas_sdk.preview.models.workflow_recovery_automatic_blocked_notification import (
    WorkflowRecoveryAutomaticBlockedNotification,
)
from faas_sdk.preview.models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry
from faas_sdk.preview.models.workflow_recovery_queue_blocked_webhook_payload import WorkflowRecoveryQueueBlockedWebhookPayload


def test_initial_attention_and_history_round_trip():
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    event = {
        "event_id": ident,
        "app_id": ident,
        "app": "orders",
        "review_id": ident,
        "action_id": ident,
        "policy_version": 1,
        "position": 1,
        "status": "applied",
        "reason": "terminal_outcome_not_saved",
        "observed_at": stamp,
        "queue_url": "/queue",
        "review_status_url": "/initial-recovery-reviews/review",
        "initial_failure_id": ident,
        "state": "blocked",
        "evidence_kind": "initial",
        "run_id": ident,
        "resume_count": 1,
        "repairable": True,
        "diagnosis_reason": "terminal_outcome_not_saved",
        "evidence_url": "/initial-recovery-reviews/review/evidence",
        "cli_command": "gregale automations failure-recovery-evidence --app orders --review review",
    }
    assert WorkflowRecoveryQueueBlockedWebhookPayload.from_dict(event).to_dict() == event
    receipt = {
        "event_id": ident,
        "status": "no_receivers",
        "reason": "terminal_outcome_not_saved",
        "captured_at": stamp,
        "receiver_webhook_ids": [],
        "evidence_kind": "initial",
    }
    assert WorkflowRecoveryAutomaticBlockedNotification.from_dict(receipt).to_dict() == receipt
    entry = {
        "id": "audit:" + ident,
        "occurred_at": stamp,
        "kind": "initial_evidence_repaired",
        "outcome": "failed",
        "reason": "restore proof",
        "durable": True,
        "request_id": ident,
        "initial_failure_id": ident,
        "evidence_kind": "initial",
        "resume_count": 1,
    }
    assert WorkflowRecoveryHistoryEntry.from_dict(entry).to_dict() == entry
