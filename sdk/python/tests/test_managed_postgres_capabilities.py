import httpx

from faas_sdk.api.managed_postgres import get_managed_postgres_capabilities
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.managed_postgres_capabilities import ManagedPostgresCapabilities


def test_capabilities_keep_reader_support_separate_from_rollout() -> None:
    capabilities = {
        "contract_version": 1,
        "region": "eu-central-1",
        "provisioning_enabled": False,
        "database_limit": 1,
        "postgres_majors": [16, 17],
        "service_classes": ["development"],
        "availability": ["single_zone"],
        "credential_access": ["read_only", "read_write", "migration"],
        "scale_to_zero": True,
        "always_on": False,
        "pooled_connections": True,
        "point_in_time_restore": True,
        "storage_limit_bytes": 10737418240,
        "restore_window_seconds": 604800,
    }
    requests = []

    def respond(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        assert request.method == "GET"
        assert request.url.path == "/v1/postgres/capabilities"
        assert dict(request.url.params) == {"region": "eu-central-1"}
        assert request.headers["authorization"] == "Bearer fixture-token"
        return httpx.Response(200, json=capabilities)

    client = AuthenticatedClient(
        base_url="https://api.example.test",
        token="fixture-token",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    with client:
        result = get_managed_postgres_capabilities.sync(client=client, region="eu-central-1")
    assert isinstance(result, ManagedPostgresCapabilities)
    assert result.to_dict() == capabilities
    assert len(requests) == 1
