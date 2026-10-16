from faas_sdk.models import WorkflowRecoveryInitialPolicyChangePreview
from faas_sdk.models.workflow_recovery_policy_change_preview import WorkflowRecoveryPolicyChangePreview


def test_initial_policy_change_round_trip_with_execution_pressure():
    app = "11111111-1111-4111-8111-111111111111"
    failure = "22222222-2222-4222-8222-222222222222"
    run = "33333333-3333-4333-8333-333333333333"
    stamp = "2026-10-12T12:00:00+00:00"
    policy = {
        "enabled": True,
        "mode": "automatic_initial",
        "workflow_names": ["recover"],
        "triggers": ["initial_failure"],
        "failure_statuses": ["dead"],
        "max_resume_count": 1,
        "cooldown_seconds": 60,
        "hourly_action_budget": 10,
        "hourly_execution_budget": 1,
        "pause_after_failures": 1,
        "rate_per_second": 1,
        "reason": "proposed recovery",
    }
    initial = {
        "coverage": "partial",
        "inspected_failures": 1,
        "truncated": True,
        "eligible": 1,
        "excluded": 0,
        "safety_blocked": 0,
        "would_prepare": 1,
        "would_submit": 0,
        "retained_reviews": 1,
        "remaining_review_capacity": 999,
        "active_recovery_jobs": 0,
        "remaining_recovery_job_capacity": 3,
        "unresolved_recovery": False,
        "runtime_available": True,
        "candidates": [
            {
                "failure": {
                    "id": failure,
                    "app_id": app,
                    "run_id": run,
                    "workflow": "recover",
                    "failure_status": "dead",
                    "observed_at": stamp,
                },
                "eligibility": "eligible",
                "preparation": "would_prepare",
                "execution": "hourly_execution_budget",
                "blockers": [],
                "target": {"run_id": run, "expected_resume_count": 0, "failure_evidence_id": failure},
            }
        ],
    }
    assert WorkflowRecoveryInitialPolicyChangePreview.from_dict(initial).to_dict() == initial
    body = {
        "current": {
            "app_id": app,
            "version": 2,
            "policy": policy,
            "updated_at": stamp,
            "actor_kind": "api_key",
            "actor_id": "owner",
            "status": "monitoring",
        },
        "proposed": policy,
        "observed_at": stamp,
        "next_version": 3,
        "pending_suppressed": 0,
        "outstanding_retained": 0,
        "entries": [],
        "entries_truncated": False,
        "automatic_execution_enabled": True,
        "hourly_executions": 1,
        "remaining_execution_budget": 0,
        "hourly_prepared_actions": 1,
        "remaining_preparation_budget": 9,
        "inspected_jobs": 0,
        "jobs_truncated": False,
        "candidates": [],
        "candidates_truncated": False,
        "initial_failures": initial,
    }
    assert WorkflowRecoveryPolicyChangePreview.from_dict(body).to_dict() == body
    del body["initial_failures"]
    assert WorkflowRecoveryPolicyChangePreview.from_dict(body).to_dict() == body
