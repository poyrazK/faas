"""Generated container clients preserve listener request and status contracts."""

import json

import httpx

from faas_sdk.api.apps import app_tcp_listener_tls_status, create_app_udp_listener, update_app_tcp_listener
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.create_udp_listener_request import CreateUDPListenerRequest
from faas_sdk.models.tcp_listener_tls_config import TCPListenerTLSConfig
from faas_sdk.models.update_tcp_listener_request import UpdateTCPListenerRequest
from faas_sdk.types import UNSET


def test_tcp_policy_mutations_have_typed_bodies():
    enabled = update_app_tcp_listener._get_kwargs("app", "echo", body=UpdateTCPListenerRequest(enabled=True))
    assert enabled["json"] == {"enabled": True}
    tls = update_app_tcp_listener._get_kwargs(
        "app",
        "echo",
        body=UpdateTCPListenerRequest(tls=TCPListenerTLSConfig(mode="terminate", hostname="echo.example")),
    )
    assert tls["json"] == {"tls": {"mode": "terminate", "hostname": "echo.example"}}
    assert tls["method"] == "patch"


def test_udp_create_keeps_public_port_optional():
    kwargs = create_app_udp_listener._get_kwargs("app", body=CreateUDPListenerRequest(name="dns", guest_port=5353))
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/apps/app/udp-listeners"
    assert kwargs["json"] == {"name": "dns", "guest_port": 5353}


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


def test_udp_create_preserves_explicit_automatic_allocation():
    body = CreateUDPListenerRequest(name="dns", guest_port=5353, public_port=0)
    kwargs = create_app_udp_listener._get_kwargs("app", body=body)
    assert kwargs["json"] == {"name": "dns", "guest_port": 5353, "public_port": 0}
    assert CreateUDPListenerRequest.from_dict(kwargs["json"]).public_port == 0
