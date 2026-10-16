from faas_sdk.models.workflow_recovery_history import WorkflowRecoveryHistory


def test_evidence_history_round_trip():
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    entry = {
        "id": "audit:" + ident,
        "occurred_at": stamp,
        "kind": "retry_child_evidence_repaired",
        "outcome": "failed",
        "reason": "restore verified outcome",
        "durable": True,
        "evidence_kind": "child",
        "resume_count": 2,
        "request_id": ident,
        "review_id": ident,
        "action_id": ident,
        "job_id": ident,
        "child_job_id": ident,
        "run_id": ident,
        "actor_kind": "api_key",
        "actor_id": "operator",
        "review_url": "/reviews/review",
        "evidence_url": "/reviews/review/actions/action/evidence",
        "job_url": "/v1/event-recoveries/child",
    }
    body = {
        "observed_at": stamp,
        "created_after": stamp,
        "created_before": stamp,
        "entries": [entry],
        "retained_observations": True,
    }
    assert WorkflowRecoveryHistory.from_dict(body).to_dict() == body
    entry.update(
        kind="retry_evidence_cleared",
        notification_event_id=ident,
        diagnosis_reason="already_recorded",
        reason="blocker_cleared",
        outcome="recovered",
    )
    del entry["request_id"]
    assert WorkflowRecoveryHistory.from_dict(body).to_dict() == body
    body["entries"] = [
        {"id": "old", "occurred_at": stamp, "kind": "retry_result", "outcome": "failed", "reason": "saved_failure"}
    ]
    assert WorkflowRecoveryHistory.from_dict(body).to_dict() == body
