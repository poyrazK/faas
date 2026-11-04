from faas_sdk.preview.models.event_recovery_request import EventRecoveryRequest
from faas_sdk.preview.models.workflow_initial_recovery_preview import WorkflowInitialRecoveryPreview


def test_initial_failure_preview_target_survives_recovery_submission():
    failure_id = "11111111-1111-4111-8111-111111111111"
    run_id = "22222222-2222-4222-8222-222222222222"
    body = {
        "failure": {
            "id": failure_id,
            "app_id": "33333333-3333-4333-8333-333333333333",
            "run_id": run_id,
            "workflow": "recover",
            "failure_status": "dead",
            "observed_at": "2026-10-12T12:00:00+00:00",
        },
        "observed_at": "2026-10-12T12:01:00+00:00",
        "reason": "eligible",
        "blockers": [],
        "target": {
            "run_id": run_id,
            "expected_resume_count": 0,
            "failure_evidence_id": failure_id,
        },
    }
    preview = WorkflowInitialRecoveryPreview.from_dict(body)
    assert preview.to_dict() == body
    request = EventRecoveryRequest.from_dict(
        {
            "request_id": "44444444-4444-4444-8444-444444444444",
            "mode": "workflow",
            "workflow_runs": {"name": "recover", "runs": [preview.target.to_dict()]},
        }
    )
    assert request.to_dict()["workflow_runs"]["runs"] == [body["target"]]
    del body["target"]
    body["reason"] = "capacity"
    body["blockers"] = ["active_run_quota"]
    assert "target" not in WorkflowInitialRecoveryPreview.from_dict(body).to_dict()
