import asyncio
import json
from uuid import UUID

import httpx
import pytest

from faas_sdk.api.deployments import get_cell_image_release, put_cell_image_release
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import PutCellImageReleaseRequest

CELL_ID = UUID("7c93ae23-83ed-4dc9-987a-4676cd3ca6c2")
APP_ID = UUID("6cb3f3a7-8b0d-4f6c-82d9-2c2b61ad1983")
RELEASE_ID = UUID("4225983b-5e81-477c-a77d-c3f7e302ef87")


def verify_request(request, method):
    assert request.method == method
    assert request.url.path == f"/v1/cells/{CELL_ID}/apps/{APP_ID}/image-releases/{RELEASE_ID}"
    assert request.headers["Authorization"] == "Bearer regional-key"
    if method == "PUT":
        assert json.loads(request.content) == {"image": "image-marker"}


@pytest.mark.parametrize("method", ["PUT", "GET"])
@pytest.mark.parametrize("status", [307, 308])
def test_cell_image_release_sync_refuses_redirect(method, status):
    requests = []

    def handler(request):
        requests.append(request)
        return httpx.Response(status, headers={"Location": "/recipient"})

    client = AuthenticatedClient(base_url="https://cell.example.com", token="regional-key", follow_redirects=True)
    with httpx.Client(
        base_url="https://cell.example.com",
        headers={"Authorization": "Bearer regional-key"},
        transport=httpx.MockTransport(handler),
        follow_redirects=True,
    ) as transport:
        client.set_httpx_client(transport)
        if method == "PUT":
            response = put_cell_image_release.sync_detailed(
                CELL_ID, APP_ID, RELEASE_ID, client=client, body=PutCellImageReleaseRequest(image="image-marker")
            )
        else:
            response = get_cell_image_release.sync_detailed(CELL_ID, APP_ID, RELEASE_ID, client=client)
    assert response.status_code == status
    assert len(requests) == 1
    verify_request(requests[0], method)


@pytest.mark.parametrize("method", ["PUT", "GET"])
@pytest.mark.parametrize("status", [307, 308])
def test_cell_image_release_async_refuses_redirect(method, status):
    async def exercise():
        requests = []

        def handler(request):
            requests.append(request)
            return httpx.Response(status, headers={"Location": "/recipient"})

        client = AuthenticatedClient(base_url="https://cell.example.com", token="regional-key", follow_redirects=True)
        async with httpx.AsyncClient(
            base_url="https://cell.example.com",
            headers={"Authorization": "Bearer regional-key"},
            transport=httpx.MockTransport(handler),
            follow_redirects=True,
        ) as transport:
            client.set_async_httpx_client(transport)
            if method == "PUT":
                response = await put_cell_image_release.asyncio_detailed(
                    CELL_ID, APP_ID, RELEASE_ID, client=client, body=PutCellImageReleaseRequest(image="image-marker")
                )
            else:
                response = await get_cell_image_release.asyncio_detailed(CELL_ID, APP_ID, RELEASE_ID, client=client)
        assert response.status_code == status
        assert len(requests) == 1
        verify_request(requests[0], method)

    asyncio.run(exercise())


@pytest.mark.parametrize("asynchronous", [False, True])
def test_cell_image_release_transport_retains_tombstone(asynchronous):
    def handler(request):
        verify_request(request, "GET")
        return httpx.Response(
            200,
            json={
                "cell_id": str(CELL_ID),
                "region": "eu-fsn",
                "regional_account_id": str(CELL_ID),
                "regional_app_id": str(APP_ID),
                "release_id": str(RELEASE_ID),
                "image": "image-marker",
                "deployment_id": None,
                "deployment_status": "expired",
                "created_at": "2026-10-04T00:00:00Z",
                "replayed": False,
            },
        )

    client = AuthenticatedClient(base_url="https://cell.example.com", token="regional-key")
    if asynchronous:

        async def exercise():
            async with httpx.AsyncClient(
                base_url="https://cell.example.com",
                headers={"Authorization": "Bearer regional-key"},
                transport=httpx.MockTransport(handler),
            ) as transport:
                client.set_async_httpx_client(transport)
                return await get_cell_image_release.asyncio_detailed(CELL_ID, APP_ID, RELEASE_ID, client=client)

        response = asyncio.run(exercise())
    else:
        with httpx.Client(
            base_url="https://cell.example.com",
            headers={"Authorization": "Bearer regional-key"},
            transport=httpx.MockTransport(handler),
        ) as transport:
            client.set_httpx_client(transport)
            response = get_cell_image_release.sync_detailed(CELL_ID, APP_ID, RELEASE_ID, client=client)
    assert response.status_code == 200
    assert response.parsed.deployment_id is None
    assert response.parsed.deployment_status == "expired"
    assert response.parsed.release_id == RELEASE_ID
