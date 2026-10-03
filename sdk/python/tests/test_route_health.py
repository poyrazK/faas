from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_route_health_gate, get_route_health_report, set_route_health_gate
from faas_sdk.models import RouteHealthGate, RouteHealthReport, SetRouteHealthGateRequest


def test_route_health_requests_and_evidence():
    app = "00000000-0000-4000-8000-000000000001"
    candidate = "00000000-0000-4000-8000-000000000002"
    stable = "00000000-0000-4000-8000-000000000003"
    at = "2026-10-02T20:00:00+00:00"
    selectors = [{"method": "POST", "path": "/checkout", "check_latency": True, "max_p95_ms": 300}]
    gate = {
        "app_id": app,
        "mode": "enforce",
        "on_regression": "abort",
        "revision": 1,
        "routes": selectors,
        "updated_at": at,
    }
    window = {
        "start": "2026-10-02T19:57:00+00:00",
        "end": "2026-10-02T19:58:00+00:00",
        "candidate": {"requests": 100, "server_errors": 10, "error_rate": 0.1, "p95_latency_ms": 500.0},
        "stable": {"requests": 100, "server_errors": 0, "error_rate": 0.0, "p95_latency_ms": 100.0},
        "status": "regressed",
        "reason": "server_error_rate_increased",
        "error_status": "regressed",
        "error_reason": "server_error_rate_increased",
        "latency_status": "regressed",
        "latency_reason": "latency_budget_and_regression",
        "latency_delta_ms": 400.0,
        "latency_factor": 5.0,
    }
    report = {
        "app_id": app,
        "deployment_id": candidate,
        "candidate_commit_sha": "a" * 40,
        "stable_deployment_id": stable,
        "stable_commit_sha": "b" * 40,
        "canary_step": 0,
        "mode": "enforce",
        "on_regression": "abort",
        "revision": 1,
        "checked_at": at,
        "coverage": "observed_only",
        "status": "regressed",
        "reason": "consecutive_server_error_regression",
        "minimum_requests": 20,
        "minimum_latency_requests": 100,
        "routes": [
            {
                "method": "POST",
                "path": "/checkout",
                "check_latency": True,
                "max_p95_ms": 300,
                "status": "regressed",
                "reason": "consecutive_server_error_regression",
                "error_status": "regressed",
                "error_reason": "consecutive_server_error_regression",
                "latency_status": "regressed",
                "latency_reason": "consecutive_latency_violation",
                "windows": [window, {**window, "start": window["end"], "end": "2026-10-02T19:59:00+00:00"}],
            }
        ],
    }
    assert RouteHealthGate.from_dict(gate).to_dict() == gate
    assert RouteHealthReport.from_dict(report).to_dict() == report
    request = SetRouteHealthGateRequest.from_dict(
        {"mode": "enforce", "on_regression": "abort", "expected_revision": 0, "routes": selectors}
    )

    def respond(req: httpx.Request) -> httpx.Response:
        assert req.headers["Authorization"] == "Bearer token"
        if req.url.path == "/v1/apps/demo/route-health/gate":
            if req.method == "PUT":
                assert req.content == httpx.Request("PUT", "https://example.test", json=request.to_dict()).content
            else:
                assert req.method == "GET"
            return httpx.Response(200, json=gate)
        assert req.method == "GET"
        assert req.url.path == f"/v1/apps/demo/route-health/deployments/{candidate}"
        return httpx.Response(200, json=report)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    assert isinstance(get_route_health_gate.sync("demo", client=client.inner), RouteHealthGate)
    assert isinstance(set_route_health_gate.sync("demo", client=client.inner, body=request), RouteHealthGate)
    got = get_route_health_report.sync("demo", UUID(candidate), client=client.inner)
    assert isinstance(got, RouteHealthReport)
    assert got.routes[0].windows[0].candidate.server_errors == 10
    assert got.routes[0].windows[0].candidate.p95_latency_ms == 500.0
    assert got.routes[0].windows[0].latency_factor == 5.0
    assert got.routes[0].max_p95_ms == 300
    assert got.minimum_latency_requests == 100
    assert got.stable_deployment_id == stable
    client.httpx_client.close()


def test_route_latency_zero_and_legacy_selectors():
    from faas_sdk.models import RouteHealthCounts, RouteHealthRoute
    from faas_sdk.types import Unset

    legacy = {"method": "POST", "path": "/checkout"}
    assert RouteHealthRoute.from_dict(legacy).to_dict() == legacy
    counts = {"requests": 100, "server_errors": 0, "error_rate": 0.0, "p95_latency_ms": 0.0}
    assert RouteHealthCounts.from_dict(counts).p95_latency_ms == 0.0
    counts.pop("p95_latency_ms")
    assert isinstance(RouteHealthCounts.from_dict(counts).p95_latency_ms, Unset)
