from faas_sdk.preview.models.register_scenario_test_request import RegisterScenarioTestRequest
from faas_sdk.preview.models.scenario_suspend_resume_evidence import ScenarioSuspendResumeEvidence
import pytest


@pytest.mark.parametrize("checkpoint", ["after_setup", "managed_job"])
def test_suspend_resume_plan_and_original_owner_proof_round_trip(checkpoint):
    spec = {"version": 1, "checkpoint": "after_setup", "workloads": ["worker"], "pause_ms": 1000, "within_ms": 2000}
    spec["checkpoint"] = checkpoint
    if checkpoint == "managed_job":
        spec["worker_checkpoint"] = "after-commit"
    request = {"members": [{"workload": "worker", "app_slug": "isolated-worker"}], "suspend_resume": spec}
    parsed_request = RegisterScenarioTestRequest.from_dict(request)
    assert parsed_request.suspend_resume.pause_ms == 1000
    assert parsed_request.to_dict() == request
    receipt = {
        "id": "11111111-1111-4111-8111-111111111111",
        "account_id": "22222222-2222-4222-8222-222222222222",
        "app_id": "33333333-3333-4333-8333-333333333333",
        "run_id": "a" * 32,
        "workload": "worker",
        "deployment_id": "44444444-4444-4444-8444-444444444444",
        "instance_id": "55555555-5555-4555-8555-555555555555",
        "recorder_session_id": "66666666-6666-4666-8666-666666666666",
        "state": "complete", "method": "in_place", "pause_ms": 1000, "resume_ms": 120,
    }
    proof = {"version": 1, "run_id": "a" * 32, "spec": spec, "plan_sha256": "b" * 64, "status": "complete", "receipts": [receipt]}
    if checkpoint == "managed_job":
        receipt.update(invocation_id="77777777-7777-4777-8777-777777777777", invocation_attempt=1, checkpoint_attempt_id="88888888-8888-4888-8888-888888888888")
    parsed = ScenarioSuspendResumeEvidence.from_dict(proof)
    assert parsed.spec.within_ms == 2000
    assert str(parsed.receipts[0].recorder_session_id) == receipt["recorder_session_id"]
    assert parsed.receipts[0].resume_ms == 120
    if checkpoint == "managed_job":
        assert parsed.spec.worker_checkpoint == "after-commit"
        assert parsed.receipts[0].invocation_attempt == 1
        assert str(parsed.receipts[0].invocation_id) == receipt["invocation_id"]
    assert parsed.to_dict() == proof
