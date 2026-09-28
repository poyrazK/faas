from __future__ import annotations

from faas_sdk.models import RequestAnalyticsDependency, RequestAnalyticsRoute


def test_request_analytics_dependency_round_trips_deployment_regression() -> None:
    payload = {
        "type": "outbound_integration",
        "kind": "https",
        "name": "stripe.charge",
        "samples": 40,
        "calls": 40,
        "error_calls": 20,
        "error_rate_pct": 50.0,
        "p50_ms": 80,
        "p95_ms": 150,
        "p99_ms": 150,
        "exclusive_p95_ms": 150,
        "deployment_observations": [
            {
                "deployment_id": "deploy-v2",
                "deployment_tag": "v2",
                "deployment_created_at": "2026-09-25T11:00:00Z",
                "samples": 20,
                "calls": 20,
                "error_calls": 20,
                "error_rate_pct": 100.0,
                "p50_ms": 150,
                "p95_ms": 150,
                "p99_ms": 150,
                "exclusive_p95_ms": 150,
                "p95_change_pct": 87.5,
                "error_rate_change_pct": 100.0,
                "compared_to": "v1",
                "regression": True,
            }
        ],
    }

    dependency = RequestAnalyticsDependency.from_dict(payload)

    assert dependency.deployment_observations[0].regression is True
    assert dependency.deployment_observations[0].compared_to == "v1"
    assert dependency.to_dict() == payload


def test_request_analytics_route_round_trips_deployment_compute_observations() -> None:
    payload = {
        "route": "/checkout",
        "method": "POST",
        "requests": 60,
        "error_requests": 1,
        "error_rate_pct": 1.67,
        "cold_boots": 2,
        "p50_ms": 41,
        "p95_ms": 183,
        "p99_ms": 250,
        "deployment_observations": [
            {
                "deployment_id": "deploy-v39",
                "deployment_tag": "v39",
                "deployment_created_at": "2026-09-25T11:00:00Z",
                "requests": 58,
                "request_share_pct": 58.0,
                "estimated_compute_cost_millicents": 92000,
                "guest_cpu_avg_ms": 21,
                "guest_cpu_measured_requests": 23,
                "guest_cpu_change_pct": 61.0,
                "guest_cpu_compared_to": "v38",
                "guest_cpu_regression": True,
            }
        ],
        "other_deployment_requests": 2,
        "other_deployment_estimated_compute_cost_millicents": 3000,
        "estimated_compute_cost_millicents": 95000,
        "request_share_pct": 60.0,
    }

    route = RequestAnalyticsRoute.from_dict(payload)

    observation = route.deployment_observations[0]
    assert observation.guest_cpu_regression is True
    assert observation.guest_cpu_compared_to == "v38"
    assert route.other_deployment_requests == 2
    assert route.to_dict() == payload
