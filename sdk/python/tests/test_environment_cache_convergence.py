"""Convergence clients retain exact environment/revision and fleet counts."""

from uuid import UUID

import httpx
import pytest

from faas_sdk.api.edge_rules import get_environment_cache_convergence
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import EnvironmentCacheConvergence


@pytest.mark.parametrize(
    "status,ack,pending,stale", [("converged", 2, 0, 0), ("pending", 1, 1, 0), ("degraded", 1, 0, 1)]
)
def test_environment_cache_convergence_identity_and_counts(status, ack, pending, stale):
    environment = "00000000-0000-4000-8000-000000000003"

    def respond(request):
        assert request.url.path == "/v1/apps/catalogue/cache-policy/convergence"
        assert request.url.params == httpx.QueryParams({"environment": "staging"})
        return httpx.Response(
            200,
            json={
                "app_id": "00000000-0000-4000-8000-000000000001",
                "project_id": "00000000-0000-4000-8000-000000000002",
                "environment_id": environment,
                "scope": "staging",
                "revision": 2,
                "status": status,
                "checked_at": "2026-10-15T15:00:00Z",
                "serving_gateways": 2,
                "acknowledged_gateways": ack,
                "pending_gateways": pending,
                "stale_gateways": stale,
            },
        )

    client = AuthenticatedClient(
        base_url="https://example.test", token="fixture", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    result = get_environment_cache_convergence.sync("catalogue", client=client, environment="staging")
    assert isinstance(result, EnvironmentCacheConvergence)
    assert result.environment_id == UUID(environment) and result.revision == 2
    assert result.status == status
    assert result.acknowledged_gateways + result.pending_gateways + result.stale_gateways == result.serving_gateways
