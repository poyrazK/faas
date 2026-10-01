import json

import httpx

from faas_sdk.api.apps import app_tcp_listener_tls_status
from faas_sdk.client import AuthenticatedClient
from faas_sdk.types import UNSET


def test_tls_status_parses_unknown_without_inventing_expiry():
    wire = {
        "name": "echo",
        "tls": {"mode": "terminate", "hostname": "echo.example"},
        "enabled": True,
        "scope": "observed_edges",
        "observations": [{"edge_id": "edge-one", "status": "unknown", "observed_at": "2026-10-01T12:00:00Z"}],
    }

    def respond(request):
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/app/tcp-listeners/echo/tls-status"
        assert request.headers["authorization"] == "Bearer fixture-token"
        return httpx.Response(200, content=json.dumps(wire))

    client = AuthenticatedClient(
        base_url="https://api.example", token="fixture-token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.get_httpx_client():
        result = app_tcp_listener_tls_status.sync("app", "echo", client=client)
    assert result is not None
    assert result.scope == "observed_edges"
    assert result.observations[0].status == "unknown"
    assert result.observations[0].not_after is UNSET
