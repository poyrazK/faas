from faas_sdk.api.events import diagnose_workflow_retry_evidence, repair_workflow_retry_evidence
from faas_sdk.models.workflow_initial_evidence_repair_request import WorkflowInitialEvidenceRepairRequest
from faas_sdk.models.workflow_recovery_policy_review import WorkflowRecoveryPolicyReview
from faas_sdk.models.workflow_retry_evidence_diagnosis import WorkflowRetryEvidenceDiagnosis


def test_retry_diagnosis_and_repair_routes_round_trip():
    ident = "11111111-1111-4111-8111-111111111111"
    stamp = "2026-10-13T12:00:00+00:00"
    receipt = {
        "request_id": ident,
        "reason": "verified retry",
        "actor_kind": "api_key",
        "actor_id": "operator",
        "repaired_at": stamp,
        "outcome": "failed",
    }
    body = {
        "review_id": ident,
        "action_id": ident,
        "job_id": ident,
        "run_id": ident,
        "resume_count": 2,
        "observed_at": stamp,
        "reason": "already_recorded",
        "repairable": False,
        "repair": receipt,
    }
    assert WorkflowRetryEvidenceDiagnosis.from_dict(body).to_dict() == body
    req = WorkflowInitialEvidenceRepairRequest.from_dict({"request_id": ident, "reason": "verified retry"})
    kwargs = repair_workflow_retry_evidence._get_kwargs("orders", ident, ident, body=req)
    assert kwargs["method"] == "post"
    assert kwargs["json"] == req.to_dict()
    assert kwargs["url"].endswith("/reviews/" + ident + "/actions/" + ident + "/evidence/repair")
    kwargs = diagnose_workflow_retry_evidence._get_kwargs("orders", ident, ident)
    assert kwargs["method"] == "get"
    assert kwargs["url"].endswith("/actions/" + ident + "/evidence")
    parent = {
        "id": ident,
        "app_id": ident,
        "job_id": ident,
        "policy_version": 3,
        "policy": {
            "enabled": True,
            "mode": "automatic_retry",
            "workflow_names": ["recover"],
            "triggers": ["saved_failure"],
            "failure_statuses": ["failed"],
            "max_resume_count": 4,
            "cooldown_seconds": 60,
            "hourly_action_budget": 10,
            "hourly_execution_budget": 2,
            "pause_after_failures": 2,
            "rate_per_second": 1,
            "reason": "incident",
        },
        "observed_at": stamp,
        "decisions": [],
        "evidence_repairs": {ident: receipt},
    }
    assert WorkflowRecoveryPolicyReview.from_dict(parent).to_dict() == parent
    del parent["evidence_repairs"]
    assert WorkflowRecoveryPolicyReview.from_dict(parent).to_dict() == parent
