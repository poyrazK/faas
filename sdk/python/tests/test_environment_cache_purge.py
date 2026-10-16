"""Purge SDK keeps explicit whole-host targets and stable operation identity."""

import json

import httpx

from faas_sdk.api.edge_rules import create_environment_cache_purge, get_environment_cache_purge
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import (
    CreateEnvironmentCachePurgeRequest,
    EnvironmentCachePurgeReceipt,
    EnvironmentCachePurgeStatus,
)


def test_environment_purge_retry_and_status():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
        "request_id": "00000000-0000-4000-8000-000000000004",
    }
    proposal = {**identity, "path": ""}
    receipt = {
        **proposal,
        "scope": "staging",
        "generation": 7,
        "requested_at": "2026-10-15T19:00:00Z",
    }
    writes = []

    def respond(request):
        assert request.url.params == httpx.QueryParams({"environment": "staging"})
        if request.method == "POST":
            body = json.loads(request.content)
            assert body == proposal
            writes.append(body)
            return httpx.Response(200, json=receipt)
        assert request.method == "GET"
        assert request.url.path.endswith(identity["request_id"])
        return httpx.Response(
            200,
            json={
                **receipt,
                "status": "completed",
                "checked_at": "2026-10-15T19:00:05Z",
                "serving_gateways": 2,
                "acknowledged_gateways": 2,
                "pending_gateways": 0,
                "stale_gateways": 0,
            },
        )

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    body = CreateEnvironmentCachePurgeRequest.from_dict(proposal)
    for _ in range(2):
        saved = create_environment_cache_purge.sync("catalogue", client=client, environment="staging", body=body)
        assert isinstance(saved, EnvironmentCachePurgeReceipt)
        assert saved.path == "" and saved.generation == 7
    assert writes[0] == writes[1]
    status = get_environment_cache_purge.sync(
        "catalogue",
        identity["request_id"],
        client=client,
        environment="staging",
    )
    assert isinstance(status, EnvironmentCachePurgeStatus)
    assert status.status == "completed" and status.acknowledged_gateways == 2
