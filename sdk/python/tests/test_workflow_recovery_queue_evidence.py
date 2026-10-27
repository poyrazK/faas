from faas_sdk.preview.models.workflow_recovery_automatic_queue_entry import WorkflowRecoveryAutomaticQueueEntry


def test_queue_evidence_round_trip_and_legacy_fields():
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    body = {
        "review_id": ident,
        "policy_version": 1,
        "queued_at": stamp,
        "queued_age_seconds": 0,
        "action": {"action_id": ident, "position": 1, "status": "pending", "reason": "hourly_budget"},
    }
    assert WorkflowRecoveryAutomaticQueueEntry.from_dict(body).to_dict() == body
    body.update(
        wait_category="budget",
        wait_reason="hourly_budget",
        evidence={
            "diagnosis": {
                "review_id": ident,
                "action_id": ident,
                "job_id": ident,
                "run_id": ident,
                "resume_count": 1,
                "observed_at": stamp,
                "reason": "terminal_outcome_not_saved",
                "repairable": True,
                "evidence_kind": "parent",
                "item_position": 1,
            },
            "evidence_url": "/reviews/review/actions/action/evidence/parent",
            "cli_command": "gregale automations recovery-retry-evidence --app orders --parent",
        },
    )
    assert WorkflowRecoveryAutomaticQueueEntry.from_dict(body).to_dict() == body
    del body["evidence"]
    body["evidence_unavailable_reason"] = "review_not_retained"
    assert WorkflowRecoveryAutomaticQueueEntry.from_dict(body).to_dict() == body
