import json
from pathlib import Path
from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import (
    get_route_monitor,
    get_route_monitor_incident,
    get_route_monitor_report,
    list_route_monitor_incidents,
    set_route_monitor,
)
from faas_sdk.models import RouteMonitorConfig, RouteMonitorIncident, SetRouteMonitorRequest


def test_production_route_monitor_preserves_budget_and_saved_evidence():
    payload = json.loads(
        (Path(__file__).resolve().parents[3] / "tests/fixtures/production-route-incident.json").read_text()
    )
    assert RouteMonitorIncident.from_dict(payload).to_dict() == payload
    config = {
        "app_id": payload["app_id"],
        "enabled": True,
        "revision": 1,
        "routes": [{"method": "POST", "path": "/checkout", "max_5xx_rate_bps": 0, "max_p95_ms": 300}],
    }
    calls = []

    def respond(req: httpx.Request) -> httpx.Response:
        calls.append(req)
        assert req.url.path.startswith("/v1/apps/demo/route-monitor")
        if req.method == "PUT":
            assert json.loads(req.content) == {"enabled": True, "expected_revision": 0, "routes": config["routes"]}
            result = config
        elif req.url.path.endswith("/report"):
            result = payload["opening_report"]
        elif req.url.path.endswith("/incidents/" + payload["id"]):
            result = payload
        elif req.url.path.endswith("/incidents"):
            assert req.url.params["limit"] == "5"
            assert req.url.params["before"] == payload["id"]
            result = {"app_id": payload["app_id"], "incidents": [payload]}
        else:
            result = config
        return httpx.Response(200, json=result)

    client = FaaSClient(
        base_url="https://example.test", token="test", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.httpx_client:
        assert get_route_monitor.sync("demo", client=client.inner).to_dict() == config
        request = SetRouteMonitorRequest.from_dict(
            {"enabled": True, "expected_revision": 0, "routes": config["routes"]}
        )
        assert set_route_monitor.sync("demo", client=client.inner, body=request).to_dict() == config
        assert get_route_monitor_report.sync("demo", client=client.inner).to_dict() == payload["opening_report"]
        assert list_route_monitor_incidents.sync(
            "demo", client=client.inner, limit=5, before=UUID(payload["id"])
        ).to_dict() == {"app_id": payload["app_id"], "incidents": [payload]}
        assert get_route_monitor_incident.sync("demo", UUID(payload["id"]), client=client.inner).to_dict() == payload
        assert len(calls) == 5
        assert RouteMonitorConfig.from_dict(config).routes[0].max_5xx_rate_bps == 0
