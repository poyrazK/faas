from faas_sdk.preview.models.register_scenario_test_request import RegisterScenarioTestRequest
from faas_sdk.preview.models.scenario_cold_start_evidence import ScenarioColdStartEvidence


def test_cold_start_budget_and_host_receipts_are_typed_and_round_trip():
    spec = {"version": 1, "within_ms": 1000}
    request = {"members": [{"workload": "primary", "app_slug": "isolated-api"}], "cold_start": spec}
    parsed_request = RegisterScenarioTestRequest.from_dict(request)
    assert parsed_request.cold_start.within_ms == 1000
    assert parsed_request.to_dict() == request
    receipt = {
        "id": "11111111-1111-4111-8111-111111111111",
        "account_id": "22222222-2222-4222-8222-222222222222",
        "app_id": "33333333-3333-4333-8333-333333333333",
        "run_id": "a" * 32,
        "workload": "primary",
        "deployment_id": "44444444-4444-4444-8444-444444444444",
        "state": "booted",
        "boot_instance_id": "55555555-5555-4555-8555-555555555555",
        "boot_recorder_session_id": "66666666-6666-4666-8666-666666666666",
        "wake_id": "77777777-7777-4777-8777-777777777777",
        "method": "cold_boot",
        "startup_ms": 120,
    }
    proof = {"version": 1, "run_id": "a" * 32, "spec": spec, "plan_sha256": "b" * 64, "status": "complete", "preparations": [receipt]}
    parsed = ScenarioColdStartEvidence.from_dict(proof)
    assert parsed.spec.within_ms == 1000
    assert str(parsed.preparations[0].boot_instance_id) == receipt["boot_instance_id"]
    assert parsed.preparations[0].startup_ms == 120
    assert parsed.to_dict() == proof
