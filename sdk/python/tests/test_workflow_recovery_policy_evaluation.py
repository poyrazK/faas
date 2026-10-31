import httpx

from faas_sdk.preview.api.events import evaluate_workflow_recovery_policy
from faas_sdk.preview.models.workflow_recovery_policy_evaluation import WorkflowRecoveryPolicyEvaluation
from faas_sdk.preview.models.workflow_recovery_policy_evaluation_request import WorkflowRecoveryPolicyEvaluationRequest


def policy(statuses=None):
    return {
        "enabled": True,
        "mode": "preview",
        "workflow_names": ["orders"],
        "triggers": ["saved_failure"],
        "failure_statuses": statuses or [],
        "max_resume_count": 3,
        "cooldown_seconds": 60,
        "hourly_action_budget": 10,
        "rate_per_second": 1,
        "reason": "evaluate settings",
    }


def totals():
    return {
        "candidates": 1,
        "would_prepare": 0,
        "excluded": 0,
        "unknown": 1,
        "cooldown": 0,
        "preparation_budget": 0,
        "deduplicated": 0,
        "execution_budget_pressure": 0,
        "execution_cooldown_pressure": 0,
    }


def test_historical_policy_evaluation_roundtrip_and_read_only_endpoint():
    timestamp = "2026-10-11T00:00:00+00:00"
    proposed = policy(["failed"])
    request_body = {
        "expected_version": 4,
        "policy": proposed,
        "workflow": "orders",
        "created_after": "2026-10-04T00:00:00+00:00",
        "created_before": timestamp,
    }
    request = WorkflowRecoveryPolicyEvaluationRequest.from_dict(request_body)
    assert request.to_dict() == request_body
    kwargs = evaluate_workflow_recovery_policy._get_kwargs("consumer", body=request)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/apps/consumer/recovery-policy/evaluate"
    assert kwargs["json"] == request_body
    assessment = {
        "eligibility": "unknown",
        "reason": "failure_status_ambiguous",
        "preparation": "unknown",
        "execution_pressure": "not_evaluated",
    }
    identifier = "11111111-1111-4111-8111-111111111111"
    body = {
        "failure_coverage": {
            "capture_started_at": timestamp,
            "coverage": "partial",
            "captured_failures": 1,
            "reviewed_failures": 1,
            "unreviewed_failures": 0,
            "missing_eligibility_evidence": 0,
            "truncated": False,
        },
        "current": {
            "app_id": identifier,
            "version": 4,
            "policy": policy(["failed", "dead"]),
            "updated_at": timestamp,
            "actor_kind": "account",
            "actor_id": "operator",
            "status": "monitoring",
        },
        "proposed": proposed,
        "observed_at": timestamp,
        "created_after": request_body["created_after"],
        "created_before": timestamp,
        "inspected_reviews": 1,
        "reviews_truncated": False,
        "candidates_truncated": False,
        "executions_truncated": False,
        "observed_executions": 0,
        "current_totals": totals(),
        "proposed_totals": totals(),
        "candidates": [
            {
                "evidence_source": "policy_review",
                "review_id": identifier,
                "job_id": identifier,
                "run_id": identifier,
                "workflow": "orders",
                "observed_at": timestamp,
                "policy_version": 3,
                "position": 1,
                "resume_count": 1,
                "trigger": "saved_failure",
                "recorded_outcome": "prepared",
                "historical_failure_statuses": ["failed", "dead"],
                "observed_hourly_executions": 0,
                "execution_evidence_ids": [],
                "current": assessment,
                "proposed": assessment,
            }
        ],
        "coverage_notes": ["conditional replay from empty state"],
    }
    assert WorkflowRecoveryPolicyEvaluation.from_dict(body).to_dict() == body
    response = httpx.Response(200, json=body)
    parsed = evaluate_workflow_recovery_policy._parse_response(
        client=type("Client", (), {"raise_on_unexpected_status": True})(), response=response
    )
    assert parsed.to_dict() == body
    candidate = body["candidates"][0]
    candidate["last_observed_execution_at"] = timestamp
    candidate["execution_evidence_ids"] = ["audit:proof"]
    assert WorkflowRecoveryPolicyEvaluation.from_dict(body).to_dict() == body

    candidate["evidence_source"] = "failure_inventory"
    candidate["failure_evidence_id"] = "failure:proof"
    for key in ("review_id", "job_id", "position", "policy_version"):
        candidate.pop(key)
    candidate["recorded_outcome"] = "captured_failure"
    body["failure_coverage"].update(reviewed_failures=0, unreviewed_failures=1, missing_eligibility_evidence=1)
    assert WorkflowRecoveryPolicyEvaluation.from_dict(body).to_dict() == body

    candidate["later_eligibility"] = {
        "observation": {
            "observed_at": timestamp,
            "parent_job_id": identifier,
            "parent_position": 1,
            "reason": "eligible",
            "blockers": [],
        },
        "freshness": "fresh",
        "current": assessment,
        "proposed": assessment,
    }
    assert WorkflowRecoveryPolicyEvaluation.from_dict(body).to_dict() == body
    candidate["later_eligibility"]["freshness"] = "stale"
    candidate["later_eligibility"]["observation"]["blockers"] = ["operation_owned"]
    assert WorkflowRecoveryPolicyEvaluation.from_dict(body).to_dict() == body
