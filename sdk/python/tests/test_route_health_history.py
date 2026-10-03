from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_route_health_history_entry, list_route_health_history
from faas_sdk.models import RouteHealthHistoryEntry, RouteHealthHistoryPage
from faas_sdk.types import Unset


def test_saved_route_health_history_requests_and_roundtrip():
    candidate = "00000000-0000-4000-8000-000000000002"
    decision = "00000000-0000-4000-8000-000000000004"
    at = "2026-10-02T20:00:00+00:00"
    policy = {
        "version": 1,
        "windows": 2,
        "window_seconds": 60,
        "ingestion_lag_seconds": 30,
        "minimum_requests": 20,
        "minimum_errors": 2,
        "error_rate_floor": 0.05,
        "error_rate_delta": 0.05,
        "error_rate_factor": 3.0,
        "minimum_latency_requests": 100,
        "latency_quantile": 0.95,
        "latency_factor": 1.5,
        "latency_delta_ms": 100.0,
        "comparison_epsilon": 1e-12,
    }
    report = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "deployment_id": candidate,
        "candidate_commit_sha": "a" * 40,
        "stable_deployment_id": "00000000-0000-4000-8000-000000000003",
        "stable_commit_sha": "b" * 40,
        "canary_step": 0,
        "mode": "enforce",
        "revision": 1,
        "checked_at": at,
        "observation_anchor": "2026-10-02T19:00:00+00:00",
        "coverage": "observed_only",
        "status": "unknown",
        "reason": "comparisons_incomplete_or_unsettled",
        "minimum_requests": 20,
        "minimum_latency_requests": 100,
        "routes": [
            {
                "method": "POST",
                "path": "/checkout",
                "max_p95_ms": 300,
                "status": "unknown",
                "reason": "insufficient_latency_requests",
                "windows": [
                    {
                        "start": "2026-10-02T19:57:00+00:00",
                        "end": "2026-10-02T19:58:00+00:00",
                        "candidate": {"requests": 100, "server_errors": 0, "error_rate": 0.0, "p95_latency_ms": 0.0},
                        "stable": {"requests": 0, "server_errors": 0, "error_rate": 0.0},
                        "status": "unknown",
                        "reason": "insufficient_latency_requests",
                    }
                ],
            }
        ],
    }
    entry = {
        "version": 1,
        "id": decision,
        "checked_at": at,
        "source": "worker",
        "traffic_percent": 1,
        "requested_traffic_percent": 10,
        "policy": policy,
        "report": report,
        "decision": {
            "mode": "enforce",
            "revision": 1,
            "deployment_id": candidate,
            "stable_deployment_id": report["stable_deployment_id"],
            "history_id": decision,
            "status": "blocked",
            "reason": report["reason"],
            "checked_at": at,
        },
    }
    page = {"app_id": report["app_id"], "deployment_id": candidate, "entries": [entry], "next_cursor": decision}
    assert RouteHealthHistoryEntry.from_dict(entry).to_dict() == entry
    assert RouteHealthHistoryPage.from_dict(page).to_dict() == page
    abort = {
        **entry,
        "purpose": "abort",
        "requested_traffic_percent": 0,
        "report": {**report, "on_regression": "abort", "status": "regressed"},
        "decision": {**entry["decision"], "on_regression": "abort", "status": "aborted"},
    }
    assert RouteHealthHistoryEntry.from_dict(abort).to_dict() == abort

    def respond(req: httpx.Request) -> httpx.Response:
        assert req.method == "GET"
        assert req.headers["Authorization"] == "Bearer token"
        path = f"/v1/apps/demo/route-health/deployments/{candidate}/history"
        if req.url.path == path:
            assert dict(req.url.params) == {"limit": "2", "before": decision}
            return httpx.Response(200, json=page)
        assert req.url.path == path + "/" + decision
        assert not req.url.query
        return httpx.Response(200, json=entry)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    try:
        got_page = list_route_health_history.sync(
            "demo", UUID(candidate), client=client.inner, limit=2, before=UUID(decision)
        )
        assert isinstance(got_page, RouteHealthHistoryPage)
        got = get_route_health_history_entry.sync("demo", UUID(candidate), UUID(decision), client=client.inner)
        assert isinstance(got, RouteHealthHistoryEntry)
        assert got.decision.history_id == UUID(decision)
        assert got.policy.minimum_latency_requests == 100
        assert got.report.observation_anchor.isoformat() == report["observation_anchor"]
        assert got.report.routes[0].windows[0].candidate.p95_latency_ms == 0.0
        assert isinstance(got.report.routes[0].windows[0].stable.p95_latency_ms, Unset)
    finally:
        client.httpx_client.close()
