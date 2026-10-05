"""Verify trigger control responses through the generated and public transports."""

import httpx
import pytest

from faas_sdk import FaaSClient, FaasProblemError
from faas_sdk.api.triggers import delete_trigger, pause_trigger, resume_trigger, update_trigger
from faas_sdk.models import Trigger, UpdateTriggerRequest


def test_trigger_controls_and_binding_ownership_conflicts() -> None:
    def handle(request: httpx.Request) -> httpx.Response:
        if "/triggers/consumer" in request.url.path:
            return httpx.Response(
                409,
                headers={"Content-Type": "application/problem+json"},
                json={
                    "title": "Queue consumer is binding-owned",
                    "status": 409,
                    "code": "validation_failed",
                    "detail": "manage this consumer through its queue binding",
                },
            )
        return httpx.Response(
            200,
            json={
                "id": "broker",
                "account_id": "account",
                "app_id": "app",
                "kind": "queue",
                "slug": "jobs",
                "enabled": request.url.path.endswith("/resume"),
                "config": {"mode": "queue"},
                "batch_size_max": 1,
                "batch_window_ms": 1000,
                "max_attempts": 3,
                "payload_max_bytes": 1024,
                "broker_poison_strategy": "commit",
                "created_at": "2026-10-01T00:00:00Z",
                "updated_at": "2026-10-01T00:00:00Z",
            },
        )

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        paused = pause_trigger.sync("broker", client=client.inner)
        resumed = resume_trigger.sync("broker", client=client.inner)
        assert isinstance(paused, Trigger) and paused.enabled is False
        assert isinstance(resumed, Trigger) and resumed.enabled is True
        for mutate in (
            lambda: pause_trigger.sync("consumer", client=client.inner),
            lambda: resume_trigger.sync("consumer", client=client.inner),
            lambda: update_trigger.sync("consumer", client=client.inner, body=UpdateTriggerRequest(enabled=False)),
            lambda: delete_trigger.sync("consumer", client=client.inner),
        ):
            with pytest.raises(FaasProblemError) as caught:
                mutate()
            assert caught.value.status == 409
            assert caught.value.problem.code == "validation_failed"
    finally:
        client.close()


def test_generated_client_parses_binding_ownership_problem() -> None:
    client = httpx.Client(
        base_url="https://api.example.test",
        transport=httpx.MockTransport(
            lambda _: httpx.Response(
                409,
                json={
                    "title": "Queue consumer is binding-owned",
                    "status": 409,
                    "code": "validation_failed",
                },
            )
        ),
    )
    from faas_sdk.client import Client
    from faas_sdk.models import Problem

    generated = Client(base_url="https://api.example.test").set_httpx_client(client)
    try:
        response = pause_trigger.sync_detailed("consumer", client=generated)
        assert response.status_code == 409
        assert isinstance(response.parsed, Problem)
        assert response.parsed.code == "validation_failed"
    finally:
        client.close()
