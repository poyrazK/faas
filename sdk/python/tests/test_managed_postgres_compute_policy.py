import json
from uuid import UUID

import httpx

from faas_sdk.api.managed_postgres import (
    change_managed_postgres_compute_policy,
    get_managed_postgres_compute_policy_change,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.change_managed_postgres_compute_policy_request import ChangeManagedPostgresComputePolicyRequest
from faas_sdk.models.managed_postgres_compute_policy_change import ManagedPostgresComputePolicyChange


def test_compute_policy_uuid_replay_and_progress() -> None:
    request_id = UUID("33333333-3333-4333-8333-333333333333")
    progress = {
        "id": str(request_id),
        "database_id": "orders",
        "from_scale_to_zero": True,
        "target_scale_to_zero": False,
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
            assert request.url.path == "/v1/postgres/databases/orders/compute-policy"
            assert json.loads(request.content) == {"request_id": str(request_id), "scale_to_zero": False}
            return httpx.Response(202, json=progress)
        assert request.method == "GET"
        assert request.url.path == f"/v1/postgres/databases/orders/compute-policy-changes/{request_id}"
        return httpx.Response(200, json=progress)

    client = AuthenticatedClient(
        base_url="https://api.example.test", token="fixture", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    body = ChangeManagedPostgresComputePolicyRequest(request_id=request_id, scale_to_zero=False)
    with client:
        for _ in range(2):
            result = change_managed_postgres_compute_policy.sync("orders", client=client, body=body)
            assert isinstance(result, ManagedPostgresComputePolicyChange)
            assert result.id == request_id
        result = get_managed_postgres_compute_policy_change.sync("orders", request_id, client=client)
    assert isinstance(result, ManagedPostgresComputePolicyChange)
    assert result.connection_interruption_expected
    assert len(requests) == 3
