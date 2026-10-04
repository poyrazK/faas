"""Queue environment identity survives the generated public transport."""

import json

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.queues import create_queue_binding, get_queue_binding_status, queue_send, send_app_message
from faas_sdk.models import CreateQueueBindingRequest, QueueSendRequest, QueueSendRequestPayload, SendAppMessageRequest


def test_queue_environment_transport() -> None:
    binding_id = "00000000-0000-4000-8000-000000000001"
    environment_id = "00000000-0000-4000-8000-000000000002"
    calls: list[httpx.Request] = []

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        base = {"environment": "staging", "environment_id": environment_id}
        if request.url.path.endswith("/queues/send"):
            return httpx.Response(201, json={**base, "id": "message", "queue_binding_id": binding_id})
        if request.url.path.endswith("/inbox"):
            return httpx.Response(
                202,
                json={
                    **base,
                    "id": "message",
                    "queue_binding_id": binding_id,
                    "event_id": "event",
                    "target_app": "worker",
                    "status": "pending",
                    "status_url": "/status",
                },
            )
        if request.url.path.endswith("/status"):
            return httpx.Response(
                200,
                json={
                    **base,
                    "binding_id": binding_id,
                    "name": "orders",
                    "queue_name": "orders",
                    "mode": "push",
                    "workload_class": "worker",
                    "enabled": True,
                    "consumer_state": "paused",
                    "consumer_state_reason": "environment_unavailable",
                    "consumer_liveness": "not_observed",
                    "depth": 1,
                    "in_flight": 0,
                    "dead_letter": 0,
                    "generated_at": "2026-10-01T00:00:00Z",
                },
            )
        return httpx.Response(
            201,
            json={
                **base,
                "id": binding_id,
                "app_id": "app",
                "account_id": "00000000-0000-4000-8000-000000000003",
                "name": "orders",
                "queue_name": "orders",
                "mode": "push",
                "workload_class": "worker",
                "enabled": True,
                "max_concurrency": 1,
                "created_at": "2026-10-01T00:00:00Z",
                "updated_at": "2026-10-01T00:00:00Z",
            },
        )

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        binding = create_queue_binding.sync_detailed(
            "worker",
            client=client.inner,
            body=CreateQueueBindingRequest(name="orders", queue_name="orders", environment="staging"),
        )
        assert str(binding.parsed.environment_id) == environment_id
        assert binding.parsed.environment == "staging"
        sent = queue_send.sync_detailed(
            "worker",
            client=client.inner,
            body=QueueSendRequest(
                environment="staging", queue_name="orders", payload=QueueSendRequestPayload(), flag_context="flags"
            ),
        )
        assert sent.parsed.queue_binding_id == binding_id
        assert sent.parsed.environment == "staging"
        inbox = send_app_message.sync_detailed(
            "worker",
            client=client.inner,
            body=SendAppMessageRequest(type_="order.created", data={}, environment="staging", queue_name="orders"),
        )
        assert inbox.parsed.queue_binding_id == binding_id
        assert inbox.parsed.environment == "staging"
        status = get_queue_binding_status.sync_detailed("worker", binding_id, client=client.inner)
        assert str(status.parsed.environment_id) == environment_id
        assert status.parsed.consumer_state_reason == "environment_unavailable"
        assert len(calls) == 4
        assert all(json.loads(call.content)["environment"] == "staging" for call in calls[:3])
        assert json.loads(calls[1].content)["flag_context"] == "flags"
    finally:
        client.close()
