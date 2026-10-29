from uuid import UUID

from faas_sdk.preview.models.event_recovery_request import EventRecoveryRequest
from faas_sdk.preview.models.workflow_run_recovery_plan import WorkflowRunRecoveryPlan
from faas_sdk.preview.models.workflow_run_recovery_target import WorkflowRunRecoveryTarget


def test_durable_run_plan_preserves_reviewed_zero_resume_count():
    target = WorkflowRunRecoveryTarget(run_id=UUID("22222222-2222-4222-8222-222222222222"), expected_resume_count=0)
    request = EventRecoveryRequest(
        request_id=UUID("11111111-1111-4111-8111-111111111111"),
        mode="workflow",
        workflow_runs=WorkflowRunRecoveryPlan(name="recover", runs=[target]),
        rate_per_second=2,
    )
    body = request.to_dict()
    assert body["workflow_runs"] == {
        "name": "recover",
        "runs": [{"run_id": "22222222-2222-4222-8222-222222222222", "expected_resume_count": 0}],
    }
    assert EventRecoveryRequest.from_dict(body).to_dict() == body
    assert "workflow_runs" not in EventRecoveryRequest().to_dict()


def test_native_recovery_item_and_coverage_are_readable():
    from faas_sdk.preview.models.event_recovery_item import EventRecoveryItem
    from faas_sdk.preview.models.event_recovery_job_coverage import check_event_recovery_job_coverage

    body = {
        "position": 1,
        "event_source": "",
        "event_id": "",
        "event_type": "",
        "subscription_id": "",
        "failed_at": "2026-10-10T00:00:00+00:00",
        "failure_code": "dead",
        "retryable": False,
        "state": "queued",
        "workflow_run_id": "22222222-2222-4222-8222-222222222222",
        "expected_resume_count": 0,
        "execution": {
            "observed_at": "2026-10-10T00:00:02+00:00",
            "state": "succeeded",
            "source": "workflow_run",
            "attempts": 0,
            "completed_at": "2026-10-10T00:00:01+00:00",
        },
    }
    assert EventRecoveryItem.from_dict(body).to_dict() == body
    assert check_event_recovery_job_coverage("reviewed_workflow_runs") == "reviewed_workflow_runs"


def test_retained_native_result_preserves_evidence_after_pruning():
    from faas_sdk.preview.models.event_recovery_execution import EventRecoveryExecution

    body = {
        "observed_at": "2026-10-10T00:00:03+00:00",
        "state": "failed",
        "source": "recovery_result",
        "evidence_source": "workflow_run",
        "attempts": 0,
        "completed_at": "2026-10-10T00:00:01+00:00",
        "recorded_at": "2026-10-10T00:00:02+00:00",
    }
    assert EventRecoveryExecution.from_dict(body).to_dict() == body


def test_native_completion_notification_supports_expired_admission_and_items_link():
    from faas_sdk.preview.models.event_recovery_execution_finished_webhook_payload import (
        EventRecoveryExecutionFinishedWebhookPayload,
    )
    from faas_sdk.preview.models.event_recovery_execution_job_health_state import (
        check_event_recovery_execution_job_health_state,
    )

    job_id = "11111111-1111-4111-8111-111111111111"
    timestamp = "2026-10-10T00:00:03+00:00"
    execution = dict.fromkeys(
        [
            "queued",
            "running",
            "retrying",
            "succeeded",
            "dead_lettered",
            "expired",
            "cancelled",
            "superseded",
            "unknown",
        ],
        0,
    )
    execution.update(observed_at=timestamp, tracked_count=1, saved_results=1, failed=1)
    body = {
        "event_id": "22222222-2222-4222-8222-222222222222",
        "job_id": job_id,
        "app_id": "33333333-3333-4333-8333-333333333333",
        "mode": "workflow",
        "state": "expired",
        "outcome": "finished_with_non_success",
        "selected_count": 2,
        "pending_count": 0,
        "queued_count": 1,
        "skipped_count": 0,
        "cancelled_count": 1,
        "created_at": timestamp,
        "expires_at": timestamp,
        "completed_at": timestamp,
        "execution": execution,
        "execution_finished_at": timestamp,
        "unresolved_count": 0,
        "items_url": f"/v1/event-recoveries/{job_id}/items",
    }
    assert EventRecoveryExecutionFinishedWebhookPayload.from_dict(body).to_dict() == body
    assert check_event_recovery_execution_job_health_state("expired") == "expired"


def test_reviewed_retry_preserves_parent_position_and_resume_count():
    request = EventRecoveryRequest(
        request_id=UUID("11111111-1111-4111-8111-111111111111"),
        parent_job_id=UUID("33333333-3333-4333-8333-333333333333"),
        mode="workflow",
        workflow_runs=WorkflowRunRecoveryPlan(
            name="recover",
            runs=[
                WorkflowRunRecoveryTarget(
                    run_id=UUID("22222222-2222-4222-8222-222222222222"),
                    expected_resume_count=1,
                    parent_position=2,
                )
            ],
        ),
    )
    body = request.to_dict()
    assert body["parent_job_id"] == "33333333-3333-4333-8333-333333333333"
    assert body["workflow_runs"]["runs"][0]["parent_position"] == 2
    assert body["workflow_runs"]["runs"][0]["expected_resume_count"] == 1
    assert EventRecoveryRequest.from_dict(body).to_dict() == body


def test_retry_preflight_preserves_waits_and_original_reviewed_count():
    from faas_sdk.preview.models.event_recovery_preflight import EventRecoveryPreflight

    timestamp = "2026-10-11T00:00:00+00:00"
    body = {
        "job_id": "11111111-1111-4111-8111-111111111111",
        "app_id": "33333333-3333-4333-8333-333333333333",
        "workflow_name": "recover",
        "review_mode": "workflow_retry",
        "reviewed_count": 1,
        "observed_at": timestamp,
        "state": "expired",
        "active": False,
        "pending_count": 0,
        "eligible_count": 0,
        "waiting_count": 1,
        "likely_skipped_count": 0,
        "unknown_count": 0,
        "reason_counts": {"capacity": 1},
        "capacity_scopes": {"app": 1},
        "rate_per_second": 2,
        "remaining_lifetime_seconds": 0.0,
        "minimum_drain_seconds": 0.0,
        "earliest_drain_at": timestamp,
        "fits_before_expiry": False,
        "assumes_immediate_resume": False,
        "sample": [
            {
                "position": 1,
                "status": "waiting",
                "reason": "capacity",
                "capacity_scope": "app",
                "workflow_run_id": "22222222-2222-4222-8222-222222222222",
                "expected_resume_count": 1,
                "saved_outcome": "failed",
                "blockers": ["active_run_quota"],
            }
        ],
    }
    assert EventRecoveryPreflight.from_dict(body).to_dict() == body


def test_execution_attention_policy_and_payload_roundtrip():
    from faas_sdk.preview.models.event_recovery_notification_kind import check_event_recovery_notification_kind
    from faas_sdk.preview.models.workflow_recovery_execution_attention_webhook_payload import (
        WorkflowRecoveryExecutionAttentionWebhookPayload,
    )

    plan = WorkflowRunRecoveryPlan(
        name="recover",
        runs=[WorkflowRunRecoveryTarget(run_id=UUID("22222222-2222-4222-8222-222222222222"), expected_resume_count=0)],
        execution_deadline_seconds=3600,
        execution_stall_seconds=900,
    )
    assert WorkflowRunRecoveryPlan.from_dict(plan.to_dict()).to_dict() == plan.to_dict()
    assert check_event_recovery_notification_kind("attention") == "attention"
    body = {
        "event_id": "11111111-1111-4111-8111-111111111111",
        "job_id": "22222222-2222-4222-8222-222222222222",
        "app_id": "33333333-3333-4333-8333-333333333333",
        "observed_at": "2026-10-11T08:00:00Z",
        "items_url": "/v1/event-recoveries/22222222-2222-4222-8222-222222222222/items",
        "attention": {
            "observed_at": "2026-10-11T08:00:00Z",
            "deadline_seconds": 3600,
            "stall_seconds": 900,
            "stalled_count": 1,
            "deadline_exceeded_count": 0,
            "capacity_waiting_count": 0,
            "scheduled_waiting_count": 0,
            "unknown_count": 0,
            "needs_attention": True,
            "notification_captured": True,
            "targets": [
                {
                    "position": 1,
                    "run_id": "44444444-4444-4444-8444-444444444444",
                    "resume_count": 1,
                    "status": "stalled",
                    "run_url": "/v1/workflows/runs/44444444-4444-4444-8444-444444444444",
                    "diagnostics_url": "/v1/workflows/runs/44444444-4444-4444-8444-444444444444/diagnostics",
                }
            ],
        },
    }
    payload = WorkflowRecoveryExecutionAttentionWebhookPayload.from_dict(body)
    assert payload.attention.targets[0].status == "stalled"
    assert payload.to_dict()["attention"]["needs_attention"] is True
    assert WorkflowRecoveryExecutionAttentionWebhookPayload.from_dict(payload.to_dict()).to_dict() == payload.to_dict()


def test_reviewed_cancellation_and_audit_target_roundtrip():
    from faas_sdk.preview.models.event_recovery_history_entry import EventRecoveryHistoryEntry
    from faas_sdk.preview.models.workflow_recovery_cancellation_request import WorkflowRecoveryCancellationRequest
    from faas_sdk.preview.models.workflow_recovery_cancellation_response import WorkflowRecoveryCancellationResponse

    request = WorkflowRecoveryCancellationRequest(position=2, expected_resume_count=3, reason="provider incident")
    assert request.to_dict() == {"position": 2, "expected_resume_count": 3, "reason": "provider incident"}
    assert WorkflowRecoveryCancellationRequest.from_dict(request.to_dict()).to_dict() == request.to_dict()
    result = WorkflowRecoveryCancellationResponse.from_dict(
        {
            "job_id": "11111111-1111-4111-8111-111111111111",
            "position": 2,
            "resume_count": 3,
            "outcome": "already_cancelled",
            "preview": False,
        }
    )
    assert result.to_dict()["outcome"] == "already_cancelled"
    history = EventRecoveryHistoryEntry.from_dict(
        {
            "id": 2,
            "occurred_at": "2026-10-11T08:00:00Z",
            "action": "workflow_cancelled",
            "actor_kind": "api_key",
            "actor_id": "review-key",
            "reason": "provider incident",
            "previous_state": "completed",
            "state": "completed",
            "previous_rate": 1,
            "rate": 1,
            "target_position": 2,
            "target_resume_count": 3,
            "target_run_id": "22222222-2222-4222-8222-222222222222",
        }
    )
    body = history.to_dict()
    assert body["target_position"] == 2
    assert body["target_resume_count"] == 3
    assert EventRecoveryHistoryEntry.from_dict(body).to_dict() == body


def test_saved_response_action_receipt_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_action_receipt import WorkflowRecoveryActionReceipt

    body = {
        "request": {
            "request_id": "11111111-1111-4111-8111-111111111111",
            "position": 1,
            "expected_resume_count": 2,
            "reason": "incident",
        },
        "response": {
            "job_id": "22222222-2222-4222-8222-222222222222",
            "position": 1,
            "resume_count": 2,
            "outcome": "changed",
            "preview": False,
        },
        "captured_at": "2026-10-11T08:00:00Z",
        "actor_kind": "api_key",
        "actor_id": "operator",
    }
    receipt = WorkflowRecoveryActionReceipt.from_dict(body)
    assert str(receipt.request.request_id) == body["request"]["request_id"]
    assert receipt.response.outcome == "changed"
    assert WorkflowRecoveryActionReceipt.from_dict(receipt.to_dict()).to_dict() == receipt.to_dict()


def test_preview_policy_and_prepared_review_roundtrip():
    from faas_sdk.preview.models.workflow_recovery_policy_review import WorkflowRecoveryPolicyReview

    body = {
        "id": "11111111-1111-4111-8111-111111111111",
        "app_id": "22222222-2222-4222-8222-222222222222",
        "job_id": "33333333-3333-4333-8333-333333333333",
        "policy_version": 1,
        "policy": {
            "enabled": True,
            "mode": "preview",
            "workflow_names": ["recover"],
            "triggers": ["saved_failure"],
            "failure_statuses": [],
            "max_resume_count": 3,
            "cooldown_seconds": 300,
            "hourly_action_budget": 10,
            "rate_per_second": 1,
            "reason": "incident",
        },
        "observed_at": "2026-10-11T09:00:00Z",
        "decisions": [
            {
                "position": 1,
                "run_id": "44444444-4444-4444-8444-444444444444",
                "resume_count": 1,
                "trigger": "saved_failure",
                "action": "retry",
                "outcome": "hourly_budget",
            }
        ],
    }
    review = WorkflowRecoveryPolicyReview.from_dict(body)
    from faas_sdk.types import UNSET

    assert review.plan is UNSET
    assert review.policy.mode == "preview"
    assert review.decisions[0].outcome == "hourly_budget"
    assert WorkflowRecoveryPolicyReview.from_dict(review.to_dict()).to_dict() == review.to_dict()


def test_retained_recovery_history_preserves_cursor_and_associations():
    from faas_sdk.preview.models.workflow_recovery_history import WorkflowRecoveryHistory

    timestamp = "2026-10-11T00:00:00+00:00"
    body = {
        "observed_at": timestamp,
        "created_after": timestamp,
        "created_before": timestamp,
        "retained_observations": True,
        "next_after": "opaque-cursor",
        "entries": [
            {
                "id": "retry_result:action",
                "occurred_at": timestamp,
                "kind": "retry_result",
                "outcome": "succeeded",
                "reason": "",
                "policy_version": 2,
                "run_id": "run",
                "child_job_id": "child",
            }
        ],
    }
    assert WorkflowRecoveryHistory.from_dict(body).to_dict() == body


def test_durable_recovery_history_preserves_frozen_policy_and_incident_metadata():
    from faas_sdk.preview.models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry

    timestamp = "2026-10-11T00:00:00+00:00"
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
    body = {
        "id": "audit:event",
        "occurred_at": timestamp,
        "kind": "policy_changed",
        "outcome": "monitoring",
        "reason": "incident",
        "durable": True,
        "policy_version": 2,
        "actor_kind": "account",
        "actor_id": "operator",
        "policy": policy,
    }
    assert WorkflowRecoveryHistoryEntry.from_dict(body).to_dict() == body
    body.pop("policy")
    body.update(kind="health_alert_captured", episode_id="11111111-1111-4111-8111-111111111111")
    assert WorkflowRecoveryHistoryEntry.from_dict(body).to_dict() == body
    body.pop("episode_id")
    body.update(kind="retry_transition", evaluation_reason="suppressed", reason="changed")
    assert WorkflowRecoveryHistoryEntry.from_dict(body).to_dict() == body
