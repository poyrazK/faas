"""Scoped advice clients preserve policy availability across API upgrades."""

import httpx
import pytest

from faas_sdk.api.apps import get_route_advice
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import RouteAdviceResponse
from faas_sdk.types import UNSET


@pytest.mark.parametrize("status", [None, "observed", "unavailable", "changed"])
def test_scoped_advice_policy_availability(status):
    def respond(request):
        assert request.url.params["environment"] == "staging"
        assert "scope" not in request.url.params
        report = {
            "slug": "shop",
            "from": "2026-10-15T00:00:00Z",
            "until": "2026-10-15T01:00:00Z",
            "cache_max_age_seconds": 60,
            "routes_analyzed": 0,
            "suggestions": [],
        }
        if status is not None:
            report["policy_context"] = {
                "status": status,
                "observed_at": "2026-10-15T01:00:00Z",
                "rules": [],
                "gaps": ["gateway_convergence_not_verified"],
            }
            if status == "observed":
                report["policy_context"]["revision"] = "a" * 64
        return httpx.Response(200, json=report)

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    result = get_route_advice.sync("shop", client=client, environment="staging")
    assert isinstance(result, RouteAdviceResponse)
    if status is None:
        assert result.policy_context is UNSET
    else:
        assert result.policy_context.status == status
        assert result.policy_context.gaps == ["gateway_convergence_not_verified"]
        if status == "observed":
            assert result.policy_context.revision == "a" * 64
        else:
            assert result.policy_context.revision is UNSET
