from faas_sdk.preview.models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry


def test_durable_candidate_facts_roundtrip():
    policy = {
        "enabled": True,
        "mode": "preview",
        "workflow_names": ["orders"],
        "triggers": ["saved_failure"],
        "failure_statuses": ["failed", "dead"],
        "max_resume_count": 3,
        "cooldown_seconds": 60,
        "hourly_action_budget": 10,
        "rate_per_second": 1,
        "reason": "provider incident",
    }
    payload = {
        "id": "audit:evidence",
        "durable": True,
        "kind": "candidates_observed",
        "occurred_at": "2026-10-11T12:00:00Z",
        "outcome": "recorded",
        "reason": "policy_review",
        "policy_version": 1,
        "workflow": "orders",
        "candidate_evidence": {
            "review_id": "00000000-0000-0000-0000-000000000001",
            "app_id": "00000000-0000-0000-0000-000000000002",
            "job_id": "00000000-0000-0000-0000-000000000003",
            "policy_version": 1,
            "policy": policy,
            "observed_at": "2026-10-11T12:00:00Z",
            "decisions": [
                {
                    "position": 1,
                    "run_id": "00000000-0000-0000-0000-000000000004",
                    "resume_count": 1,
                    "trigger": "saved_failure",
                    "action": "retry",
                    "outcome": "resume_ineligible",
                    "failure_status": "dead",
                    "preflight_outcome": "resume_ineligible",
                    "preflight_blockers": ["operation_owned"],
                }
            ],
        },
    }
    entry = WorkflowRecoveryHistoryEntry.from_dict(payload)
    decision = entry.candidate_evidence.decisions[0]
    assert decision.failure_status == "dead"
    assert decision.preflight_blockers == ["operation_owned"]
    saved = entry.to_dict()["candidate_evidence"]
    assert saved["decisions"][0]["preflight_outcome"] == "resume_ineligible"
    assert "plan" not in saved
