from uuid import UUID

from faas_sdk.preview.models.event_recovery_request import EventRecoveryRequest
from faas_sdk.preview.models.event_workflow_recovery_plan import EventWorkflowRecoveryPlan
from faas_sdk.preview.models.event_workflow_recovery_target import EventWorkflowRecoveryTarget


def test_workflow_recovery_job_preserves_reviewed_zero_generation_and_count():
    target = EventWorkflowRecoveryTarget(
        event_id="invoice", subscription_id="workflow:paid", replay_count=0, generation=0
    )
    plan = EventWorkflowRecoveryPlan(name="paid", published_version=3, source="billing", events=[target])
    request = EventRecoveryRequest(
        request_id=UUID("11111111-1111-4111-8111-111111111111"),
        workflow_routing=plan,
        rate_per_second=5,
        protect_receipts=True,
    )
    body = request.to_dict()
    assert body["workflow_routing"] == {
        "name": "paid",
        "published_version": 3,
        "source": "billing",
        "events": [{"event_id": "invoice", "subscription_id": "workflow:paid", "replay_count": 0, "generation": 0}],
    }
    assert EventRecoveryRequest.from_dict(body).to_dict() == body


def test_legacy_workflow_job_omits_generation_and_ordinary_request_omits_plan():
    target = EventWorkflowRecoveryTarget(event_id="invoice", subscription_id="workflow:paid", replay_count=2)
    assert "generation" not in target.to_dict()
    assert "workflow_routing" not in EventRecoveryRequest().to_dict()


def test_recovery_review_decision_and_live_status_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_review_decision import WorkflowRecoveryReviewDecision
    from faas_sdk.preview.models.workflow_recovery_review_status import WorkflowRecoveryReviewStatus

    decision = {
        "decision": "approved",
        "reason": "provider recovered",
        "decided_at": "2026-10-11T10:00:00+00:00",
        "actor_kind": "api_key",
        "actor_id": "key-owner",
    }
    assert WorkflowRecoveryReviewDecision.from_dict(decision).to_dict() == decision
    status = {
        "review_id": "11111111-1111-4111-8111-111111111111",
        "observed_at": "2026-10-11T10:01:00+00:00",
        "status": "stale",
        "outcome": "unknown",
        "complete": False,
        "actions": [
            {
                "position": 1,
                "kind": "cancel",
                "status": "stale",
                "reason": "already_terminal",
                "outcome": "unknown",
                "complete": False,
                "run_id": "22222222-2222-4222-8222-222222222222",
                "resume_count": 1,
                "run_url": "/v1/workflows/runs/22222222-2222-4222-8222-222222222222",
            }
        ],
    }
    assert WorkflowRecoveryReviewStatus.from_dict(status).to_dict() == status


def test_recovery_review_notification_capture_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_review_notification import WorkflowRecoveryReviewNotification

    body = {
        "event_id": "11111111-1111-4111-8111-111111111111",
        "status": "queued",
        "reason": "ready",
        "captured_at": "2026-10-11T10:00:00+00:00",
        "receiver_webhook_ids": ["22222222-2222-4222-8222-222222222222"],
    }
    assert WorkflowRecoveryReviewNotification.from_dict(body).to_dict() == body


def test_recovery_review_completion_snapshot_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_review_completion_notification import (
        WorkflowRecoveryReviewCompletionNotification,
    )
    from faas_sdk.preview.models.workflow_recovery_review_finished_webhook_payload import (
        WorkflowRecoveryReviewFinishedWebhookPayload,
    )

    result = {
        "review_id": "11111111-1111-4111-8111-111111111111",
        "observed_at": "2026-10-11T10:02:00+00:00",
        "status": "applied",
        "outcome": "cancelled",
        "complete": True,
        "actions": [
            {
                "position": 1,
                "kind": "cancel",
                "status": "applied",
                "reason": "cancelled",
                "outcome": "cancelled",
                "complete": True,
                "run_id": "22222222-2222-4222-8222-222222222222",
                "resume_count": 1,
                "run_url": "/v1/workflows/runs/22222222-2222-4222-8222-222222222222",
                "evidence_source": "cancellation_receipt",
                "recorded_at": "2026-10-11T10:01:00+00:00",
            }
        ],
    }
    body = {
        "event_id": "33333333-3333-4333-8333-333333333333",
        "status": "queued",
        "captured_at": "2026-10-11T10:02:00+00:00",
        "receiver_webhook_ids": ["44444444-4444-4444-8444-444444444444"],
        "result": result,
    }
    assert WorkflowRecoveryReviewCompletionNotification.from_dict(body).to_dict() == body
    payload = {
        "event_id": body["event_id"],
        "review_id": result["review_id"],
        "app_id": "55555555-5555-4555-8555-555555555555",
        "app": "consumer",
        "job_id": "66666666-6666-4666-8666-666666666666",
        "policy_version": 1,
        "captured_at": body["captured_at"],
        "result": result,
        "review_url": "/review",
        "status_url": "/review/status",
        "status_command": "gregale automations recovery-policy-reviews --status",
    }
    assert WorkflowRecoveryReviewFinishedWebhookPayload.from_dict(payload).to_dict() == payload


def test_automatic_policy_limits_pause_and_receipt_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from faas_sdk.preview.models.workflow_recovery_policy_automatic_execution import WorkflowRecoveryPolicyAutomaticExecution
    from faas_sdk.preview.models.workflow_recovery_policy_paused_webhook_payload import (
        WorkflowRecoveryPolicyPausedWebhookPayload,
    )
    from faas_sdk.preview.models.workflow_recovery_review_automatic_execution import WorkflowRecoveryReviewAutomaticExecution

    policy = {
        "enabled": True,
        "mode": "automatic_retry",
        "workflow_names": ["recover"],
        "triggers": ["saved_failure"],
        "failure_statuses": ["dead"],
        "max_resume_count": 3,
        "cooldown_seconds": 60,
        "hourly_action_budget": 10,
        "hourly_execution_budget": 2,
        "pause_after_failures": 2,
        "rate_per_second": 1,
        "reason": "incident",
    }
    assert WorkflowRecoveryPolicy.from_dict(policy).to_dict() == policy
    paused = {
        "consecutive_failures": 2,
        "paused_at": "2026-10-11T11:00:00+00:00",
        "pause_reason": "consecutive_failures",
    }
    assert WorkflowRecoveryPolicyAutomaticExecution.from_dict(paused).to_dict() == paused
    receipt = {
        "actions": [
            {
                "action_id": "11111111-1111-4111-8111-111111111111",
                "position": 1,
                "status": "applied",
                "reason": "child_created",
                "child_job_id": "22222222-2222-4222-8222-222222222222",
                "applied_at": "2026-10-11T10:59:00+00:00",
            }
        ]
    }
    assert WorkflowRecoveryReviewAutomaticExecution.from_dict(receipt).to_dict() == receipt
    payload = {
        "event_id": "33333333-3333-4333-8333-333333333333",
        "app_id": "44444444-4444-4444-8444-444444444444",
        "app": "consumer",
        "policy_version": 2,
        "paused_at": paused["paused_at"],
        "consecutive_failures": 2,
        "reason": "consecutive_failures",
        "policy_url": "/v1/apps/consumer/recovery-policy",
    }
    assert WorkflowRecoveryPolicyPausedWebhookPayload.from_dict(payload).to_dict() == payload


def test_recovery_operator_control_timestamps_roundtrip():
    from faas_sdk.preview.models.control_workflow_recovery_policy_request import ControlWorkflowRecoveryPolicyRequest

    control = {
        "expected_version": 4,
        "expected_updated_at": "2026-10-11T12:00:00+00:00",
        "expected_paused_at": "2026-10-11T12:01:00+00:00",
        "operation": "resume",
        "reason": "provider recovered",
    }
    assert ControlWorkflowRecoveryPolicyRequest.from_dict(control).to_dict() == control
    del control["expected_paused_at"]
    assert ControlWorkflowRecoveryPolicyRequest.from_dict(control).to_dict() == control


def test_recovery_queue_entry_and_budget_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_automatic_queue import WorkflowRecoveryAutomaticQueue

    queue = {
        "policy": {
            "app_id": "11111111-1111-4111-8111-111111111111",
            "version": 4,
            "updated_at": "2026-10-11T12:00:00+00:00",
            "actor_kind": "account",
            "actor_id": "operator",
            "status": "paused",
            "policy": {
                "enabled": True,
                "mode": "automatic_retry",
                "workflow_names": ["recover"],
                "triggers": ["saved_failure"],
                "failure_statuses": ["dead"],
                "max_resume_count": 3,
                "cooldown_seconds": 60,
                "hourly_action_budget": 10,
                "hourly_execution_budget": 2,
                "pause_after_failures": 2,
                "rate_per_second": 1,
                "reason": "incident",
            },
        },
        "observed_at": "2026-10-11T12:01:00+00:00",
        "pending": 1,
        "outstanding": 0,
        "hourly_executions": 1,
        "remaining_budget": 1,
        "truncated": False,
        "entries": [
            {
                "review_id": "22222222-2222-4222-8222-222222222222",
                "policy_version": 4,
                "queued_at": "2026-10-11T12:00:00+00:00",
                "queued_age_seconds": 60,
                "waiting_age_seconds": 30,
                "action": {
                    "action_id": "33333333-3333-4333-8333-333333333333",
                    "position": 1,
                    "status": "pending",
                    "reason": "hourly_budget",
                    "last_evaluation": {
                        "evaluated_at": "2026-10-11T12:01:00+00:00",
                        "reason": "hourly_budget",
                        "waiting_since": "2026-10-11T12:00:30+00:00",
                    },
                },
            }
        ],
    }
    assert WorkflowRecoveryAutomaticQueue.from_dict(queue).to_dict() == queue


def test_automatic_suppression_and_block_notification_roundtrip():
    from faas_sdk.preview.models.suppress_workflow_recovery_automatic_action_request import (
        SuppressWorkflowRecoveryAutomaticActionRequest,
    )
    from faas_sdk.preview.models.workflow_recovery_automatic_action import WorkflowRecoveryAutomaticAction
    from faas_sdk.preview.models.workflow_recovery_queue_blocked_webhook_payload import (
        WorkflowRecoveryQueueBlockedWebhookPayload,
    )

    request = {"expected_policy_version": 4, "reason": "handled externally"}
    assert SuppressWorkflowRecoveryAutomaticActionRequest.from_dict(request).to_dict() == request
    event_id = "11111111-1111-4111-8111-111111111111"
    action_id = "22222222-2222-4222-8222-222222222222"
    observed_at = "2026-10-11T12:00:00+00:00"
    action = {
        "action_id": action_id,
        "position": 1,
        "status": "suppressed",
        "reason": "operator_suppressed",
        "suppression": {
            "reason": "handled externally",
            "actor_kind": "api_key",
            "actor_id": "operator",
            "suppressed_at": observed_at,
        },
        "blocked_notification": {
            "event_id": event_id,
            "status": "no_receivers",
            "reason": "evidence_unknown",
            "captured_at": observed_at,
            "receiver_webhook_ids": [],
        },
    }
    assert WorkflowRecoveryAutomaticAction.from_dict(action).to_dict() == action
    payload = {
        "event_id": event_id,
        "action_id": action_id,
        "app_id": "33333333-3333-4333-8333-333333333333",
        "app": "consumer",
        "review_id": "44444444-4444-4444-8444-444444444444",
        "policy_version": 4,
        "position": 1,
        "status": "pending",
        "reason": "evidence_unknown",
        "observed_at": observed_at,
        "queue_url": "/v1/apps/consumer/recovery-policy/queue",
        "review_status_url": "/v1/apps/consumer/recovery-policy/reviews/review/status",
    }
    assert WorkflowRecoveryQueueBlockedWebhookPayload.from_dict(payload).to_dict() == payload


def test_policy_change_preview_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_policy_change_preview import WorkflowRecoveryPolicyChangePreview

    stamp = "2026-10-11T14:00:00+00:00"
    app_id = "11111111-1111-4111-8111-111111111111"
    policy = {
        "enabled": True,
        "mode": "automatic_retry",
        "workflow_names": ["recover"],
        "triggers": ["saved_failure"],
        "failure_statuses": ["dead"],
        "max_resume_count": 3,
        "cooldown_seconds": 60,
        "hourly_action_budget": 10,
        "hourly_execution_budget": 2,
        "pause_after_failures": 2,
        "rate_per_second": 1,
        "reason": "provider incident",
    }
    preview = {
        "current": {
            "app_id": app_id,
            "version": 4,
            "updated_at": stamp,
            "actor_kind": "account",
            "actor_id": "operator",
            "status": "monitoring",
            "policy": policy,
        },
        "proposed": policy,
        "observed_at": stamp,
        "next_version": 5,
        "pending_suppressed": 0,
        "outstanding_retained": 1,
        "entries": [
            {
                "effect": "retain_slot",
                "entry": {
                    "review_id": "22222222-2222-4222-8222-222222222222",
                    "policy_version": 3,
                    "queued_at": stamp,
                    "queued_age_seconds": 0,
                    "action": {
                        "action_id": "33333333-3333-4333-8333-333333333333",
                        "position": 1,
                        "status": "applied",
                        "reason": "child_created",
                        "child_job_id": "44444444-4444-4444-8444-444444444444",
                    },
                },
            }
        ],
        "entries_truncated": False,
        "automatic_execution_enabled": True,
        "hourly_executions": 1,
        "remaining_execution_budget": 1,
        "execution_cooldown_until": stamp,
        "hourly_prepared_actions": 1,
        "remaining_preparation_budget": 9,
        "preparation_cooldown_until": stamp,
        "inspected_jobs": 5,
        "jobs_truncated": True,
        "candidates": [
            {
                "job_id": "55555555-5555-4555-8555-555555555555",
                "decision": {
                    "position": 1,
                    "run_id": "66666666-6666-4666-8666-666666666666",
                    "resume_count": 2,
                    "trigger": "saved_failure",
                    "action": "retry",
                    "outcome": "already_prepared",
                },
            }
        ],
        "candidates_truncated": False,
    }
    assert WorkflowRecoveryPolicyChangePreview.from_dict(preview).to_dict() == preview
    preview["entries"] = []
    preview["candidates"] = []
    del preview["execution_cooldown_until"]
    del preview["preparation_cooldown_until"]
    assert WorkflowRecoveryPolicyChangePreview.from_dict(preview).to_dict() == preview


def test_recovery_policy_health_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_policy_health import WorkflowRecoveryPolicyHealth

    body = {
        "status": "stalled",
        "reason": "evaluation_overdue",
        "stall_after_seconds": 120,
        "sample_last_progress_at": "2026-10-11T12:00:00Z",
        "queue": {
            "policy": {
                "app_id": "11111111-1111-4111-8111-111111111111",
                "version": 0,
                "policy": {
                    "enabled": False,
                    "mode": "preview",
                    "workflow_names": [],
                    "triggers": ["saved_failure"],
                    "failure_statuses": [],
                    "max_resume_count": 3,
                    "cooldown_seconds": 300,
                    "hourly_action_budget": 10,
                    "rate_per_second": 1,
                    "reason": "",
                },
                "updated_at": "2026-10-11T12:00:00Z",
                "actor_kind": "",
                "actor_id": "",
                "status": "disabled",
            },
            "observed_at": "2026-10-11T12:00:00Z",
            "pending": 0,
            "outstanding": 1,
            "hourly_executions": 1,
            "remaining_budget": 0,
            "entries": [],
            "truncated": True,
        },
    }
    parsed = WorkflowRecoveryPolicyHealth.from_dict(body)
    assert parsed.to_dict()["status"] == "stalled"
    assert parsed.to_dict()["queue"]["truncated"] is True
    assert parsed.stall_after_seconds == 120
    body.pop("sample_last_progress_at")
    assert "sample_last_progress_at" not in WorkflowRecoveryPolicyHealth.from_dict(body).to_dict()


def test_recovery_health_alert_models_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_health_alert_policy import WorkflowRecoveryHealthAlertPolicy
    from faas_sdk.preview.models.workflow_recovery_health_incident import WorkflowRecoveryHealthIncident
    from faas_sdk.preview.models.workflow_recovery_health_webhook_payload import WorkflowRecoveryHealthWebhookPayload

    settings = {"enabled": True, "stall_after_seconds": 300, "alert_after_seconds": 60}
    assert WorkflowRecoveryHealthAlertPolicy.from_dict(settings).to_dict() == settings
    episode = "11111111-1111-4111-8111-111111111111"
    event = "22222222-2222-4222-8222-222222222222"
    app = "33333333-3333-4333-8333-333333333333"
    timestamp = "2026-10-11T15:00:00Z"
    body = {
        "episode_id": episode,
        "opened_at": timestamp,
        "candidate_since": timestamp,
        "observed_at": timestamp,
        "status": "blocked",
        "reason": "execution_quota",
        "alert_notification": {
            "event_id": event,
            "status": "no_receivers",
            "captured_at": timestamp,
            "receiver_webhook_ids": [],
        },
    }
    incident = WorkflowRecoveryHealthIncident.from_dict(body)
    assert incident.to_dict()["alert_notification"]["event_id"] == event
    assert incident.to_dict()["alert_notification"]["status"] == "no_receivers"
    assert incident.to_dict()["alert_notification"]["receiver_webhook_ids"] == []
    assert "closed_at" not in incident.to_dict()
    body["closed_at"] = timestamp
    body["status"] = "inactive"
    body["recovery_notification"] = dict(body["alert_notification"])
    assert WorkflowRecoveryHealthIncident.from_dict(body).to_dict()["recovery_notification"]["status"] == "no_receivers"
    payload = {
        "event_id": event,
        "episode_id": episode,
        "app_id": app,
        "app": "billing",
        "policy_version": 2,
        "status": "inactive",
        "reason": "automatic_execution_disabled",
        "opened_at": timestamp,
        "observed_at": timestamp,
        "pending": 0,
        "outstanding": 0,
        "queue_truncated": False,
        "health_url": "/v1/apps/billing/recovery-policy/health",
    }
    parsed = WorkflowRecoveryHealthWebhookPayload.from_dict(payload)
    assert parsed.to_dict()["event_id"] == event
    assert parsed.to_dict()["episode_id"] == episode
    assert parsed.to_dict()["status"] == "inactive"
