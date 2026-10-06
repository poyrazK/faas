import json
from uuid import UUID

import httpx

from faas_sdk.api.managed_postgres import get_managed_postgres_resize, resize_managed_postgres_database
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.managed_postgres_resize import ManagedPostgresResize
from faas_sdk.models.resize_managed_postgres_database_request import ResizeManagedPostgresDatabaseRequest


def test_resize_uuid_replay_and_progress() -> None:
    request_id = UUID("33333333-3333-4333-8333-333333333333")
    progress = {
        "id": str(request_id),
        "database_id": "orders",
        "from_class": "development",
        "target_class": "burstable",
        "generation": 2,
        "state": "pending",
        "connection_interruption_expected": True,
        "created_at": "2026-10-05T00:00:00Z",
    }
    requests = []

    def respond(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        assert request.headers["authorization"] == "Bearer fixture"
        if request.method == "POST":
            assert request.url.path == "/v1/postgres/databases/orders/resize"
            assert json.loads(request.content) == {"request_id": str(request_id), "service_class": "burstable"}
            return httpx.Response(202, json=progress)
        assert request.method == "GET"
        assert request.url.path == f"/v1/postgres/databases/orders/resizes/{request_id}"
        return httpx.Response(200, json=progress)

    client = AuthenticatedClient(
        base_url="https://api.example.test", token="fixture", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    body = ResizeManagedPostgresDatabaseRequest(request_id=request_id, service_class="burstable")
    with client:
        for _ in range(2):
            result = resize_managed_postgres_database.sync("orders", client=client, body=body)
            assert isinstance(result, ManagedPostgresResize)
            assert result.id == request_id
        result = get_managed_postgres_resize.sync("orders", request_id, client=client)
    assert isinstance(result, ManagedPostgresResize)
    assert result.connection_interruption_expected
    assert len(requests) == 3
