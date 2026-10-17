import asyncio
from datetime import UTC, datetime
from uuid import UUID

import httpx
import pytest

from faas_sdk.api.apps import list_binding_verification_runs
from faas_sdk.api.projects import list_project_binding_verification_runs
from faas_sdk.client import Client


@pytest.mark.parametrize("asynchronous", [False, True])
def test_verification_lists_filters_and_pagination(asynchronous):
    calls = []

    def handler(request):
        calls.append(request)
        assert request.method == "GET"
        assert request.url.params["limit"] == "2"
        assert request.url.params["cursor"] == "a+/="
        assert request.url.params["deployment_id"] == "22222222-2222-4222-8222-222222222222"
        if "/apps/" in request.url.path:
            assert request.url.params["include_members"] == "true"
            assert request.url.params["scope"] == "production"
        else:
            assert "include_members" not in request.url.params
        return httpx.Response(200, json=dict(runs=[], observed_at=datetime.now(UTC).isoformat(), next_cursor="next"))

    options = dict(limit=2, cursor="a+/=", deployment_id=UUID("22222222-2222-4222-8222-222222222222"), status="pending")

    async def execute():
        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handler)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            app = await list_binding_verification_runs.asyncio(
                "api", client=client, scope="production", include_members=True, **options
            )
            graph = await list_project_binding_verification_runs.asyncio(
                "project", "production", client=client, **options
            )
            assert app.next_cursor == "next" and graph.runs == []

    if asynchronous:
        asyncio.run(execute())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handler)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            app = list_binding_verification_runs.sync(
                "api", client=client, scope="production", include_members=True, **options
            )
            graph = list_project_binding_verification_runs.sync("project", "production", client=client, **options)
            assert app.next_cursor == "next" and graph.runs == []
    assert len(calls) == 2
