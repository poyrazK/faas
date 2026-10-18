import httpx

from faas_sdk.api.dev import seal_scenario_test_actions
from faas_sdk.client import Client
from faas_sdk.models.scenario_action_snapshot import ScenarioActionSnapshot


def test_simulation_fixture_and_attempt_scope_round_trip():
    from faas_sdk.models.register_scenario_test_request import RegisterScenarioTestRequest
    from faas_sdk.models.register_scenario_test_response import RegisterScenarioTestResponse

    fixture = {
        "workload": "primary", "name": "notify", "integration": "email", "adapter": "resend",
        "method": "POST", "path": "/emails", "capture_fields": ["subject"],
        "responses": [{"status": 429, "retry_after_seconds": 1}, {"status": 200, "body": '{"id":"simulated-1"}'}],
    }
    wire = {
        "command_version": 1, "webhook_mode": "capture", "network_mode": "contained", "action_evidence_version": 2,
        "members": [{"workload": "primary", "app_slug": "isolated-api"}],
        "simulation": {"version": 1, "fixtures": [fixture]},
    }
    assert RegisterScenarioTestRequest.from_dict(wire).to_dict() == wire
    ack = {"webhook_mode": "capture", "network_mode": "contained", "policy_version": 1,
           "action_evidence_version": 2, "simulation_sha256": "d" * 64, "recorder_version": 1, "command_version": 1}
    assert RegisterScenarioTestResponse.from_dict(ack).to_dict() == ack
    attempt = {
        "id": "11111111-1111-4111-8111-111111111111", "workload": "primary", "fixture": "notify",
        "integration": "email", "adapter": "resend", "method": "POST", "path": "/emails",
        "request_sha256": "e" * 64, "payload": {"subject": "Import ready"}, "attempt": 1,
        "status": "completed", "response_status": 429,
    }
    snapshot = {
        "version": 2, "scope": "managed_webhooks_and_simulated_outbound", "simulation_version": 1,
        "recorder_version": 1, "recorder_sessions": 1, "pending_recorder_sessions": 0, "incomplete_recorder_sessions": 0,
        "simulation_sha256": "d" * 64, "run_id": "a" * 32, "status": "complete",
        "workloads": ["primary"], "intents": [], "captured_count": 0, "pending_managed_work": 0,
        "incomplete_managed_effects": 0, "suppressed_deliveries": 0, "truncated": False,
        "simulated_attempts": [attempt, {**attempt, "id": "22222222-2222-4222-8222-222222222222", "attempt": 2, "response_status": 200}],
    }
    assert ScenarioActionSnapshot.from_dict(snapshot).to_dict() == snapshot


def test_sealed_action_snapshot_preserves_duplicate_business_intent():
    action = {
        "id": "11111111-1111-4111-8111-111111111111",
        "operation_id": "22222222-2222-4222-8222-222222222222",
        "webhook_id": "33333333-3333-4333-8333-333333333333",
        "workload": "primary",
        "source": "operation",
        "name": "notify",
        "type": "import.completed",
        "receiver_sha256": "c" * 64,
        "payload": {"order": 123},
    }
    duplicate = {**action, "id": "44444444-4444-4444-8444-444444444444"}
    wire = {
        "version": 1,
        "scope": "managed_operation_webhooks",
        "run_id": "a" * 32,
        "status": "complete",
        "sealed_at": "2026-10-16T21:30:00Z",
        "workloads": ["primary"],
        "intents": [action, duplicate],
        "captured_count": 2,
        "pending_managed_work": 0,
        "incomplete_managed_effects": 0,
        "suppressed_deliveries": 0,
        "truncated": False,
    }
    assert ScenarioActionSnapshot.from_dict(wire).to_dict() == {**wire, "sealed_at": "2026-10-16T21:30:00+00:00"}


def test_seal_endpoint_preserves_pending_without_marking_complete():
    wire = {
        "version": 1,
        "scope": "managed_operation_webhooks",
        "run_id": "a" * 32,
        "status": "pending",
        "workloads": ["primary"],
        "intents": [],
        "captured_count": 0,
        "pending_managed_work": 1,
        "incomplete_managed_effects": 0,
        "suppressed_deliveries": 0,
        "truncated": False,
    }
    parsed = seal_scenario_test_actions._parse_response(
        client=Client(base_url="https://api.example.com"),
        response=httpx.Response(202, json=wire),
    )
    assert isinstance(parsed, ScenarioActionSnapshot)
    assert parsed.to_dict() == wire
    kwargs = seal_scenario_test_actions._get_kwargs("a" * 32)
    assert kwargs["method"] == "post"
    assert kwargs["url"].endswith("/actions/seal")


def test_rehearsal_network_enrollment_serialization_and_acknowledgement():
    from faas_sdk.api.dev import register_scenario_test
    from faas_sdk.models.register_scenario_test_request import RegisterScenarioTestRequest
    from faas_sdk.models.register_scenario_test_response import RegisterScenarioTestResponse

    wire = {"webhook_mode": "capture", "network_mode": "contained", "members": [{"workload": "primary", "app_slug": "isolated-api"}]}
    body = RegisterScenarioTestRequest.from_dict(wire)
    assert body.to_dict() == wire
    kwargs = register_scenario_test._get_kwargs("a" * 32, body=body)
    assert kwargs["json"]["network_mode"] == "contained"
    response = register_scenario_test._parse_response(
        client=Client(base_url="https://api.example.com"),
        response=httpx.Response(200, json={"webhook_mode": "capture", "policy_version": 1, "network_mode": "contained"}),
    )
    assert isinstance(response, RegisterScenarioTestResponse)
    assert response.network_mode == "contained"


def test_simulation_recorder_pending_is_preserved_without_attempts():
    wire = {
        "version": 2, "scope": "managed_webhooks_and_simulated_outbound",
        "simulation_version": 1, "simulation_sha256": "d" * 64,
        "recorder_version": 1, "recorder_sessions": 1,
        "pending_recorder_sessions": 1, "incomplete_recorder_sessions": 0,
        "run_id": "a" * 32, "status": "pending", "workloads": ["primary"],
        "intents": [], "captured_count": 0, "pending_managed_work": 0,
        "incomplete_managed_effects": 0, "suppressed_deliveries": 0, "truncated": False,
    }
    parsed = seal_scenario_test_actions._parse_response(
        client=Client(base_url="https://api.example.com"),
        response=httpx.Response(202, json=wire),
    )
    assert isinstance(parsed, ScenarioActionSnapshot)
    assert parsed.to_dict() == wire
