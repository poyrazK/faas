from faas_sdk.models.workflow_recovery_automatic_queue_entry import WorkflowRecoveryAutomaticQueueEntry


def test_initial_queue_evidence_round_trip():
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    body = {
        "review_id": ident,
        "policy_version": 1,
        "queued_at": stamp,
        "queued_age_seconds": 0,
        "action": {"action_id": ident, "position": 1, "status": "pending", "reason": "awaiting_execution"},
        "initial_evidence": {
            "stage": "prepared",
            "diagnosis": {
                "review_id": ident,
                "run_id": ident,
                "resume_count": 1,
                "observed_at": stamp,
                "reason": "not_submitted_automatic_initial",
                "repairable": False,
            },
            "evidence_url": "/initial-recovery-reviews/review/evidence",
            "cli_command": "gregale automations failure-recovery-evidence --app orders --review review",
        },
    }
    assert WorkflowRecoveryAutomaticQueueEntry.from_dict(body).to_dict() == body
    evidence = body["initial_evidence"]
    evidence["stage"] = "submitted"
    evidence["diagnosis"].update(job_id=ident, reason="terminal_outcome_not_saved", repairable=True)
    body["action"].update(status="applied", child_job_id=ident)
    assert WorkflowRecoveryAutomaticQueueEntry.from_dict(body).to_dict() == body
    del body["initial_evidence"]
    assert WorkflowRecoveryAutomaticQueueEntry.from_dict(body).to_dict() == body
