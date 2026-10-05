"""Retained queue identities are available through the generated public API."""

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.queues import list_queue_bindings


def test_retained_queue_recovery_history() -> None:
    binding_id = "11111111-2222-4333-8444-555555555555"
    retired_at = "2026-10-01T22:50:12Z"
    calls: list[httpx.Request] = []

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        assert request.url.path == "/v1/apps/worker/queue-bindings"
        assert request.headers["Authorization"] == "Bearer token"
        rows = []
        if request.url.params.get("include_retired") == "true":
            rows = [
                {
                    "id": binding_id,
                    "app_id": "app",
                    "account_id": "00000000-0000-4000-8000-000000000003",
                    "name": "orders",
                    "queue_name": "orders",
                    "mode": "push",
                    "workload_class": "worker",
                    "environment": "production",
                    "enabled": True,
                    "max_concurrency": 1,
                    "created_at": "2026-10-01T00:00:00Z",
                    "updated_at": retired_at,
                    "retired_at": retired_at,
                }
            ]
        return httpx.Response(200, json=rows)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        assert list_queue_bindings.sync("worker", client=client.inner) == []
        history = list_queue_bindings.sync("worker", client=client.inner, include_retired=True)
        assert len(history) == 1
        assert history[0].id == binding_id
        assert history[0].retired_at.isoformat() == "2026-10-01T22:50:12+00:00"
        assert len(calls) == 2
        assert calls[0].url.params.get("include_retired") != "true"
        assert calls[1].url.params["include_retired"] == "true"
    finally:
        client.close()
