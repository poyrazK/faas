import json
from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_route_health_report, set_route_health_gate
from faas_sdk.models import RouteHealthReport, SetRouteHealthGateRequest


def test_watched_response_configuration_and_advisory_roundtrip():
    at = "2026-10-03T00:00:00+00:00"
    candidate = UUID("00000000-0000-4000-8000-000000000002")
    window = {
        "start": at,
        "end": "2026-10-03T00:01:00+00:00",
        "candidate": {"requests": 100, "responses": 20, "rate": 0.2},
        "stable": {"requests": 100, "responses": 0, "rate": 0.0},
        "status": "regressed",
        "reason": "watched_status_rate_increased",
    }
    advisory = {
        "status": "regressed",
        "reason": "consecutive_watched_status_regression",
        "minimum_requests": 20,
        "minimum_responses": 2,
        "rate_floor": 0.05,
        "rate_delta": 0.05,
        "rate_factor": 3.0,
        "statuses": [
            {
                "status_code": 403,
                "status": "regressed",
                "reason": "consecutive_watched_status_regression",
                "windows": [window, {**window, "start": window["end"], "end": "2026-10-03T00:02:00+00:00"}],
            }
        ],
    }
    payload = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "deployment_id": str(candidate),
        "candidate_commit_sha": "",
        "stable_deployment_id": "stable",
        "stable_commit_sha": "",
        "canary_step": 0,
        "mode": "report",
        "revision": 1,
        "checked_at": at,
        "coverage": "observed_only",
        "status": "healthy",
        "reason": "comparisons_healthy",
        "minimum_requests": 20,
        "client_error_status": "regressed",
        "client_error_reason": advisory["reason"],
        "routes": [
            {
                "method": "POST",
                "path": "/checkout",
                "watch_statuses": [403],
                "status": "healthy",
                "reason": "comparisons_healthy",
                "windows": [],
                "client_errors": advisory,
            }
        ],
    }
    request = SetRouteHealthGateRequest.from_dict(
        {
            "mode": "report",
            "expected_revision": 0,
            "routes": [{"method": "POST", "path": "/checkout", "watch_statuses": [403, 422]}],
        }
    )
    assert RouteHealthReport.from_dict(payload).to_dict() == payload

    def respond(req: httpx.Request) -> httpx.Response:
        if req.method == "PUT":
            assert req.url.path == "/v1/apps/demo/route-health/gate"
            body = json.loads(req.content)
            assert body["routes"][0]["watch_statuses"] == [403, 422]
            return httpx.Response(
                200, json={"app_id": payload["app_id"], "mode": "report", "revision": 1, "routes": body["routes"]}
            )
        assert req.url.path == f"/v1/apps/demo/route-health/deployments/{candidate}"
        return httpx.Response(200, json=payload)

    client = FaaSClient(
        base_url="https://example.test", token="test", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.httpx_client:
        gate = set_route_health_gate.sync("demo", client=client.inner, body=request)
        assert gate.routes[0].watch_statuses == [403, 422]
        report = get_route_health_report.sync("demo", candidate, client=client.inner)
        assert isinstance(report, RouteHealthReport)
        assert report.to_dict() == payload
