from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_route_health_report
from faas_sdk.models import GetRouteHealthReportCustomerGroupBy, RouteHealthReport


def test_customer_health_options_and_advisory_roundtrip():
    candidate = UUID("00000000-0000-4000-8000-000000000002")
    at = "2026-10-03T00:00:00+00:00"
    payload = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "deployment_id": str(candidate),
        "candidate_commit_sha": "",
        "stable_deployment_id": "stable",
        "stable_commit_sha": "",
        "canary_step": 0,
        "mode": "enforce",
        "revision": 1,
        "checked_at": at,
        "coverage": "observed_only",
        "status": "healthy",
        "reason": "comparisons_healthy",
        "minimum_requests": 20,
        "routes": [],
        "customers": {
            "group_by": "consumer",
            "details_included": True,
            "coverage": "observed_only",
            "status": "unknown",
            "reason": "customer_evidence_incomplete",
            "customers_limit": 20,
            "routes": [
                {
                    "method": "GET",
                    "path": "/orders",
                    "observed_customers": 23,
                    "customers_truncated": True,
                    "candidate": {
                        "identified_requests": 100,
                        "unattributed_requests": 7,
                        "unresolved_identity_requests": 3,
                        "other_customer_requests": 80,
                    },
                    "stable": {
                        "identified_requests": 0,
                        "unattributed_requests": 0,
                        "unresolved_identity_requests": 0,
                        "other_customer_requests": 0,
                    },
                    "customers": [
                        {
                            "customer_id": "00000000-0000-4000-8000-000000000004",
                            "health": {
                                "method": "GET",
                                "path": "/orders",
                                "status": "unknown",
                                "reason": "comparisons_incomplete_or_unsettled",
                                "windows": [
                                    {
                                        "start": at,
                                        "end": at,
                                        "status": "unknown",
                                        "reason": "insufficient_requests",
                                        "candidate": {
                                            "requests": 20,
                                            "server_errors": 10,
                                            "error_rate": 0.5,
                                            "p95_latency_ms": 500.0,
                                        },
                                        "stable": {"requests": 0, "server_errors": 0, "error_rate": 0.0},
                                    }
                                ],
                            },
                        }
                    ],
                }
            ],
        },
    }
    assert RouteHealthReport.from_dict(payload).to_dict() == payload

    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == f"/v1/apps/demo/route-health/deployments/{candidate}"
        assert request.url.params["customers"] == "true"
        assert request.url.params["customer_group_by"] == "consumer"
        assert request.url.params["customer_details"] == "true"
        return httpx.Response(200, json=payload)

    client = FaaSClient(
        base_url="https://api.example.test", token="test", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    dimension: GetRouteHealthReportCustomerGroupBy = "consumer"
    with client.httpx_client:
        report = get_route_health_report.sync(
            "demo",
            candidate,
            client=client.inner,
            customers=True,
            customer_group_by=dimension,
            customer_details=True,
        )
    assert isinstance(report, RouteHealthReport)
    assert report.to_dict() == payload
