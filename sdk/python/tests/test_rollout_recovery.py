from uuid import UUID

from faas_sdk.api.deployments import recover_rollout
from faas_sdk.models.recover_rollout_request import RecoverRolloutRequest
from faas_sdk.models.rollout_recovery_receipt import RolloutRecoveryReceipt


def test_exact_canary_recovery_request_pins_both_deployments() -> None:
    candidate = "a2b9cc53-907f-4b5c-88a4-fd0c21214556"
    predecessor = "5b87c415-7c93-4932-acab-a3c90e98be86"
    kwargs = recover_rollout._get_kwargs(
        "api",
        body=RecoverRolloutRequest(
            action="abort",
            deployment_id=UUID(candidate),
            expected_predecessor_deployment_id=UUID(predecessor),
            reason="incident",
        ),
        idempotency_key="recovery-once",
    )
    assert kwargs["url"] == "/v1/apps/api/rollouts/recover"
    assert kwargs["headers"]["Idempotency-Key"] == "recovery-once"
    assert kwargs["json"] == {
        "action": "abort",
        "deployment_id": candidate,
        "expected_predecessor_deployment_id": predecessor,
        "reason": "incident",
    }


def test_recovery_receipt_preserves_exact_restoration() -> None:
    wire = {
        "deployment_id": "a2b9cc53-907f-4b5c-88a4-fd0c21214556",
        "predecessor_deployment_id": "5b87c415-7c93-4932-acab-a3c90e98be86",
        "restored_traffic_percent": 100,
    }
    assert RolloutRecoveryReceipt.from_dict(wire).to_dict() == wire


def test_service_abort_202_preserves_acceptance_and_pending_handoff() -> None:
    import httpx

    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.rollout_transition_response import RolloutTransitionResponse
    from faas_sdk.types import UNSET

    candidate = "a2b9cc53-907f-4b5c-88a4-fd0c21214556"
    predecessor = "5b87c415-7c93-4932-acab-a3c90e98be86"
    request_id = "3e9f323a-ade6-442b-8444-c91da107fe44"
    wire = {
        "deployment": {
            "id": candidate,
            "app_id": "app",
            "image_digest": "sha256:fixture",
            "kind": "image",
            "status": "live",
            "created_at": "2026-10-05T00:00:00+00:00",
            "rollout_state": "rolling_out",
            "service_rollout_handoff": {
                "action": "abort",
                "phase": "pending",
                "retry_count": 0,
                "predecessor_deployment_id": predecessor,
                "bindings_check": {
                    "request_id": request_id,
                    "action": "abort",
                    "deployment_id": predecessor,
                    "status": "blocked",
                    "code": "bindings_check_failed",
                },
            },
        },
        "audit_id": "42",
        "service_recovery": {
            "deployment_id": candidate,
            "predecessor_deployment_id": predecessor,
            "request_id": request_id,
            "status": "accepted",
        },
    }
    parsed = recover_rollout._parse_response(
        client=AuthenticatedClient(base_url="https://fixture.invalid", token="fixture"),
        response=httpx.Response(202, json=wire),
    )
    assert isinstance(parsed, RolloutTransitionResponse)
    assert parsed.recovery is UNSET
    assert parsed.service_recovery.status == "accepted"
    assert str(parsed.service_recovery.request_id) == request_id
    assert parsed.deployment.service_rollout_handoff.phase == "pending"
    assert str(parsed.deployment.service_rollout_handoff.bindings_check.deployment_id) == predecessor
    assert parsed.to_dict() == wire
