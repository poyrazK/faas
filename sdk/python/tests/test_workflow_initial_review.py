import pytest

from faas_sdk.api.events import decide_workflow_initial_recovery_review, prepare_workflow_initial_recovery_review
from faas_sdk.models.workflow_initial_recovery_review import WorkflowInitialRecoveryReview
from faas_sdk.models.workflow_initial_recovery_review_decision_request import (
    WorkflowInitialRecoveryReviewDecisionRequest,
)
from faas_sdk.models.workflow_initial_recovery_review_request import WorkflowInitialRecoveryReviewRequest
from faas_sdk.models.workflow_recovery_review_ready_webhook_payload import WorkflowRecoveryReviewReadyWebhookPayload


@pytest.mark.parametrize(
    "mode", ["preview", "automatic_initial", "manual_result", "manual_attention", "manual_observation_failure"]
)
def test_durable_initial_review_round_trip_and_typed_submission(mode):
    failure = "11111111-1111-4111-8111-111111111111"
    run = "22222222-2222-4222-8222-222222222222"
    app = "33333333-3333-4333-8333-333333333333"
    review = "44444444-4444-4444-8444-444444444444"
    stamp = "2026-10-12T12:00:00+00:00"
    body = {
        "id": review,
        "app_id": app,
        "app": "orders",
        "failure": {
            "id": failure,
            "app_id": app,
            "run_id": run,
            "workflow": "recover",
            "failure_status": "dead",
            "observed_at": stamp,
        },
        "target": {"run_id": run, "expected_resume_count": 0, "failure_evidence_id": failure},
        "policy_version": 3,
        "policy": {
            "enabled": True,
            "mode": "preview" if mode in ("manual_result", "manual_attention", "manual_observation_failure") else mode,
            "workflow_names": ["recover"],
            "triggers": ["initial_failure"],
            "failure_statuses": ["dead"],
            "max_resume_count": 3,
            "cooldown_seconds": 60,
            "hourly_action_budget": 10,
            "rate_per_second": 1,
            "reason": "incident",
        },
        "status": "prepared",
        "reason": "incident",
        "prepared_at": stamp,
        "expires_at": "2026-10-13T12:00:00+00:00",
        "observed_at": stamp,
        "events": [
            {
                "status": "prepared",
                "reason": "incident",
                "occurred_at": stamp,
                "actor_kind": "api_key",
                "actor_id": "reviewer",
            }
        ],
    }
    if mode == "manual_observation_failure":
        body.update({"status": "submitted", "job_id": review, "submitted_at": stamp})
        body["observation_failure"] = {
            "code": "invalid_observation_data",
            "attempts": 2,
            "failed_at": stamp,
            "retry_at": "2026-10-12T12:02:00+00:00",
        }
    if mode == "automatic_initial":
        body["policy"].update({"hourly_execution_budget": 1, "pause_after_failures": 1})
        body.update({"status": "submitted", "job_id": review, "submitted_at": stamp})
        body["automatic_block_sequence"] = 2
        body["automatic_block_notification"] = {
            "event_id": review,
            "status": "queued",
            "reason": "blocker_cleared",
            "captured_at": stamp,
            "receiver_webhook_ids": [review],
        }
        body["automatic_evaluation"] = {"evaluated_at": stamp, "reason": "result_recorded"}
        body["automatic_result"] = {
            "position": 1,
            "kind": "initial_recovery",
            "status": "applied",
            "reason": "result_recorded",
            "outcome": "succeeded",
            "complete": True,
            "run_id": run,
            "resume_count": 1,
            "run_url": f"/v1/workflows/runs/{run}",
            "child_job_id": review,
            "evidence_source": "workflow_run",
            "recorded_at": stamp,
        }
    if mode == "manual_result":
        body.update({"status": "submitted", "job_id": review, "submitted_at": stamp})
        body["result"] = {
            "position": 1,
            "kind": "initial_recovery",
            "status": "applied",
            "reason": "result_recorded",
            "outcome": "failed",
            "complete": True,
            "run_id": run,
            "resume_count": 1,
            "run_url": f"/v1/workflows/runs/{run}",
            "child_job_id": review,
            "evidence_source": "workflow_run",
            "recorded_at": stamp,
        }
    if mode == "manual_attention":
        body.update({"status": "submitted", "job_id": review, "submitted_at": stamp})
        body["attention"] = {
            "reason": "evidence_unknown",
            "block_reason": "evidence_unknown",
            "sequence": 1,
            "observed_at": stamp,
            "operator_action": "Inspect retained proof",
            "evidence_url": "/evidence",
            "cli_command": "inspect",
            "diagnosis": {
                "review_id": review,
                "job_id": review,
                "run_id": run,
                "resume_count": 1,
                "observed_at": stamp,
                "reason": "job_not_retained",
                "repairable": False,
            },
            "notification": {
                "event_id": review,
                "status": "no_receivers",
                "reason": "evidence_unknown",
                "captured_at": stamp,
                "receiver_webhook_ids": [],
                "state": "blocked",
                "sequence": 1,
                "evidence_kind": "initial",
                "diagnosis_reason": "job_not_retained",
            },
        }
    model = WorkflowInitialRecoveryReview.from_dict(body)
    assert model.to_dict() == body
    request = WorkflowInitialRecoveryReviewRequest.from_dict({"expected_policy_version": 3})
    kwargs = prepare_workflow_initial_recovery_review._get_kwargs("orders", failure, body=request)
    assert kwargs["method"] == "post"
    assert kwargs["json"] == {"expected_policy_version": 3}
    decision = WorkflowInitialRecoveryReviewDecisionRequest.from_dict({"decision": "submit", "reason": "reviewed"})
    kwargs = decide_workflow_initial_recovery_review._get_kwargs("orders", review, body=decision)
    assert kwargs["json"] == {"decision": "submit", "reason": "reviewed"}
    assert kwargs["url"].endswith(f"/initial-recovery-reviews/{review}/decision")


def test_parentless_ready_notification_does_not_invent_a_job():
    body = {
        "event_id": "11111111-1111-4111-8111-111111111111",
        "review_id": "22222222-2222-4222-8222-222222222222",
        "app_id": "33333333-3333-4333-8333-333333333333",
        "app": "orders",
        "initial_failure_id": "44444444-4444-4444-8444-444444444444",
        "policy_version": 3,
        "prepared_at": "2026-10-12T12:00:00+00:00",
        "observed_at": "2026-10-12T12:00:00+00:00",
        "targets": [],
        "review_url": "/review",
        "status_url": "/review",
        "review_command": "review",
        "export_command": "review --json",
    }
    assert WorkflowRecoveryReviewReadyWebhookPayload.from_dict(body).to_dict() == body
    del body["initial_failure_id"]
    body["job_id"] = "55555555-5555-4555-8555-555555555555"
    assert WorkflowRecoveryReviewReadyWebhookPayload.from_dict(body).to_dict() == body
