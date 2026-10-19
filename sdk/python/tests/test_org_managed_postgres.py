import httpx

from faas_sdk.api.managed_postgres import (
    list_org_managed_postgres_bindings,
    list_org_managed_postgres_databases,
)
from faas_sdk.client import AuthenticatedClient


def test_company_postgres_paths_preserve_organization() -> None:
    paths: list[bytes] = []

    def respond(request: httpx.Request) -> httpx.Response:
        paths.append(request.url.raw_path)
        assert request.headers["authorization"] == "Bearer company-token"
        return httpx.Response(200, json={"items": []})

    client = AuthenticatedClient(
        base_url="https://api.example.test",
        token="company-token",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    with client:
        result = list_org_managed_postgres_databases.sync(
            organization="my company", client=client
        )
        assert result is not None
        assert result.items == []
        bindings = list_org_managed_postgres_bindings.sync(
            organization="my company", id="database/id", client=client
        )
        assert bindings is not None
        assert bindings.items == []
    assert paths == [
        b"/v1/orgs/my%20company/postgres/databases",
        b"/v1/orgs/my%20company/postgres/databases/database%2Fid/bindings",
    ]
