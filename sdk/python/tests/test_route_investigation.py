import json
from pathlib import Path
from uuid import UUID

import httpx
import pytest

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_route_health_investigation
from faas_sdk.models import (
    RouteHealthInvestigation,
)
from faas_sdk.types import UNSET


@pytest.mark.parametrize("fixture", ["route-investigation.json", "route-latency-investigation.json"])
def test_route_investigation_scope_and_weighted_evidence(fixture):
    payload = json.loads((Path(__file__).resolve().parents[3] / "tests/fixtures" / fixture).read_text())
    assert RouteHealthInvestigation.from_dict(payload).to_dict() == payload

    def respond(req: httpx.Request) -> httpx.Response:
        assert req.method == "GET"
        assert (
            req.url.path == "/v1/apps/demo/route-health/deployments/00000000-0000-4000-8000-000000000002/investigation"
        )
        assert req.url.params["method"] == "POST"
        assert req.url.params["path"] == "/checkout"
        assert req.url.params["status_code"] == str(payload["selection"]["status_code"])
        assert req.url.params.get("signal") == payload["selection"].get("signal")
        assert req.url.params["customer_group_by"] == "consumer"
        assert req.url.params["customer_id"] == payload["selection"]["customer_id"]
        return httpx.Response(200, json=payload)

    client = FaaSClient(
        base_url="https://example.test", token="test", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.httpx_client:
        report = get_route_health_investigation.sync(
            "demo",
            UUID(payload["report"]["deployment_id"]),
            client=client.inner,
            method="POST",
            path="/checkout",
            status_code=payload["selection"]["status_code"],
            signal=payload["selection"].get("signal", UNSET),
            customer_group_by="consumer",
            customer_id=UUID(payload["selection"]["customer_id"]),
        )
        assert isinstance(report, RouteHealthInvestigation)
        assert report.to_dict() == payload
