from __future__ import annotations

from faas_sdk.models import RequestAnalyticsDependency


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
