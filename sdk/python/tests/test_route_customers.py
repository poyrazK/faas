import datetime
from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_app_route_customer_usage


def test_route_customer_usage_preserves_scope_identity_and_incomplete_coverage():
    deployment = "11111111-1111-4111-8111-111111111111"
    consumer = "22222222-2222-4222-8222-222222222222"
    tenant = "33333333-3333-4333-8333-333333333333"
    at = "2026-10-02T00:00:00+00:00"
    payload = {
        "slug": "demo",
        "deployment_id": deployment,
        "from": "2026-10-01T00:00:00+00:00",
        "until": at,
        "as_of": at,
        "window_clamped": True,
        "coverage": "observed_only",
        "routes_limit": 200,
        "customers_limit": 20,
        "routes_truncated": True,
        "routes": [
            {
                "route": "GET /orders",
                "method": "GET",
                "requests": 15,
                "identified_requests": 10,
                "anonymous_requests": 5,
                "unresolved_identity_requests": 0,
                "consumer_count": 3,
                "platform_tenant_count": 1,
                "last_observed_at": at,
                "customers_truncated": True,
                "other_customer_requests": 2,
                "customers": [
                    {"consumer_id": consumer, "platform_tenant_id": tenant, "requests": 8, "last_observed_at": at}
                ],
            }
        ],
    }

    def handler(request):
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/demo/analytics/route-customers"
        assert request.url.params["deployment_id"] == deployment
        assert request.url.params["since"] == "7d"
        assert request.url.params["until"] == at
        return httpx.Response(200, json=payload)

    client = FaaSClient(
        base_url="https://api.example.com", token="test", httpx_args={"transport": httpx.MockTransport(handler)}
    )
    try:
        result = get_app_route_customer_usage.sync(
            "demo",
            client=client.inner,
            deployment_id=UUID(deployment),
            since="7d",
            until=datetime.datetime.fromisoformat(at),
        )
        assert result is not None
        assert result.to_dict() == payload
        assert result.routes[0].customers[0].platform_tenant_id == UUID(tenant)
        assert result.routes[0].customers_truncated
        assert result.routes[0].other_customer_requests == 2
    finally:
        client.httpx_client.close()


def test_route_customer_usage_tenant_only_observations_do_not_invent_consumers():
    from faas_sdk.models import RouteCustomerObservation
    from faas_sdk.types import Unset

    payload = {
        "platform_tenant_id": "33333333-3333-4333-8333-333333333333",
        "requests": 4,
        "last_observed_at": "2026-10-02T00:00:00+00:00",
    }
    result = RouteCustomerObservation.from_dict(payload)
    assert isinstance(result.consumer_id, Unset)
    assert result.to_dict() == payload
