from faas_sdk.api.events import diagnose_workflow_initial_evidence, repair_workflow_initial_evidence
from faas_sdk.models.workflow_initial_evidence_diagnosis import WorkflowInitialEvidenceDiagnosis
from faas_sdk.models.workflow_initial_evidence_repair_request import WorkflowInitialEvidenceRepairRequest


def test_verified_evidence_repair_round_trip_and_routes():
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    receipt = {
        "request_id": ident,
        "reason": "verified",
        "actor_kind": "api_key",
        "actor_id": "operator",
        "repaired_at": stamp,
        "outcome": "succeeded",
    }
    body = {
        "review_id": ident,
        "job_id": ident,
        "run_id": ident,
        "resume_count": 1,
        "observed_at": stamp,
        "reason": "already_recorded",
        "repairable": False,
        "repair": receipt,
        "execution": {
            "state": "succeeded",
            "attempts": 0,
            "source": "recovery_result",
            "observed_at": stamp,
            "completed_at": stamp,
            "recorded_at": stamp,
            "evidence_source": "workflow_run",
        },
    }
    assert WorkflowInitialEvidenceDiagnosis.from_dict(body).to_dict() == body
    request = WorkflowInitialEvidenceRepairRequest.from_dict({"request_id": ident, "reason": "verified"})
    kwargs = repair_workflow_initial_evidence._get_kwargs("orders", ident, body=request)
    assert kwargs["method"] == "post"
    assert kwargs["json"] == {"request_id": ident, "reason": "verified"}
    assert kwargs["url"].endswith("/" + ident + "/evidence/repair")
    kwargs = diagnose_workflow_initial_evidence._get_kwargs("orders", ident)
    assert kwargs["method"] == "get"
    assert kwargs["url"].endswith("/" + ident + "/evidence")
    del body["repair"]
    del body["execution"]
    body["reason"] = "job_not_retained"
    assert WorkflowInitialEvidenceDiagnosis.from_dict(body).to_dict() == body
