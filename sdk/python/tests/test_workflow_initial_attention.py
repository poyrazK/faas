import pytest

from faas_sdk.models.workflow_recovery_automatic_blocked_notification import (
    WorkflowRecoveryAutomaticBlockedNotification,
)
from faas_sdk.models.workflow_recovery_automatic_evaluation import WorkflowRecoveryAutomaticEvaluation
from faas_sdk.models.workflow_recovery_queue_blocked_webhook_payload import WorkflowRecoveryQueueBlockedWebhookPayload


@pytest.mark.parametrize(
    "reason", ["evidence_unknown", "terminal_outcome_not_saved", "execution_quota", "job_paused", "blocker_cleared"]
)
def test_initial_attention_round_trip(reason):
    stamp = "2026-10-13T12:00:00+00:00"
    ident = "11111111-1111-4111-8111-111111111111"
    body = {
        "event_id": ident,
        "app_id": ident,
        "app": "orders",
        "review_id": ident,
        "action_id": ident,
        "policy_version": 3,
        "position": 1,
        "child_job_id": ident,
        "status": "applied",
        "reason": reason,
        "observed_at": stamp,
        "queue_url": "/queue",
        "review_status_url": "/initial-recovery-reviews/" + ident,
        "initial_failure_id": ident,
        "state": "recovered" if reason == "blocker_cleared" else "blocked",
        "previous_reason": "job_paused",
        "operator_action": "Inspect job",
        "job_url": "/jobs/" + ident,
    }
    assert WorkflowRecoveryQueueBlockedWebhookPayload.from_dict(body).to_dict() == body
    receipt = {
        "event_id": ident,
        "status": "queued",
        "reason": reason,
        "captured_at": stamp,
        "receiver_webhook_ids": [ident],
    }
    assert WorkflowRecoveryAutomaticBlockedNotification.from_dict(receipt).to_dict() == receipt
    if reason != "blocker_cleared":
        evaluation = {"evaluated_at": stamp, "reason": reason}
        assert WorkflowRecoveryAutomaticEvaluation.from_dict(evaluation).to_dict() == evaluation


def test_legacy_retry_block_payload_remains_compatible():
    stamp = "2026-10-13T12:00:00+00:00"
    ident = "11111111-1111-4111-8111-111111111111"
    body = {
        "event_id": ident,
        "app_id": ident,
        "app": "orders",
        "review_id": ident,
        "action_id": ident,
        "policy_version": 1,
        "position": 1,
        "status": "pending",
        "reason": "evidence_unknown",
        "observed_at": stamp,
        "queue_url": "/queue",
        "review_status_url": "/status",
    }
    assert WorkflowRecoveryQueueBlockedWebhookPayload.from_dict(body).to_dict() == body
