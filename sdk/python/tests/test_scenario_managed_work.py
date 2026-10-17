from faas_sdk.models.register_scenario_test_request import RegisterScenarioTestRequest
from faas_sdk.models.scenario_managed_work_evidence import ScenarioManagedWorkEvidence


def test_managed_worker_capabilities_and_retry_receipts_round_trip():
    plan = {
        "version": 1,
        "primary": "primary",
        "worker_termination": {"workload": "worker", "checkpoint": "after-commit"},
        "participants": [
            {"workload": "primary", "objects": "read_write"},
            {"workload": "worker", "async": True, "objects": "read"},
        ],
    }
    request = {
        "members": [
            {"workload": "primary", "app_slug": "isolated-api"},
            {"workload": "worker", "app_slug": "isolated-worker"},
        ],
        "managed_work": plan,
    }
    assert RegisterScenarioTestRequest.from_dict(request).to_dict() == request
    worker = "33333333-3333-4333-8333-333333333333"
    deployment = "44444444-4444-4444-8444-444444444444"
    resource = "77777777-7777-4777-8777-777777777777"
    evidence = {
        "version": 1,
        "run_id": "a" * 32,
        "plan_sha256": "b" * 64,
        "status": "complete",
        "closing": True,
        "plan": plan,
        "bindings": [
            {
                "workload": "primary",
                "app_id": "11111111-1111-4111-8111-111111111111",
                "deployment_id": "22222222-2222-4222-8222-222222222222",
                "objects_resource_id": resource,
            },
            {"workload": "worker", "app_id": worker, "deployment_id": deployment, "objects_resource_id": resource},
        ],
        "jobs": [
            {
                "id": "55555555-5555-4555-8555-555555555555",
                "workload": "worker",
                "app_id": worker,
                "deployment_id": deployment,
                "method": "POST",
                "path": "/import",
                "payload_sha256": "c" * 64,
                "state": "completed",
                "attempts": [
                    {
                        "number": number,
                        "instance_id": "66666666-6666-4666-8666-666666666666",
                        "deployment_id": deployment,
                        "outcome": outcome,
                    }
                    for number, outcome in [(1, "retry"), (2, "succeeded")]
                ],
            }
        ],
    }
    evidence["jobs"][0]["attempts"][1]["instance_id"] = "77777777-7777-4777-8777-777777777777"
    evidence["worker_fault"] = {
        "id": "88888888-8888-4888-8888-888888888888",
        "account_id": worker,
        "app_id": worker,
        "run_id": evidence["run_id"],
        "workload": "worker",
        "deployment_id": deployment,
        "instance_id": evidence["jobs"][0]["attempts"][0]["instance_id"],
        "invocation_id": evidence["jobs"][0]["id"],
        "checkpoint_attempt_id": "99999999-9999-4999-8999-999999999999",
        "recorder_session_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        "state": "terminated",
        "recovery_instance_id": evidence["jobs"][0]["attempts"][1]["instance_id"],
    }
    parsed = ScenarioManagedWorkEvidence.from_dict(evidence)
    assert parsed.to_dict() == evidence
    assert str(parsed.worker_fault.recovery_instance_id) == "77777777-7777-4777-8777-777777777777"
    assert parsed.plan.worker_termination.checkpoint == "after-commit"
