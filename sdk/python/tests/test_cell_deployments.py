import asyncio
from io import BytesIO
from uuid import UUID

import httpx
import pytest

from faas_sdk.api.deployments import deploy_cell_app
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import CreateDeploymentRequest, DeployCellAppFilesBody
from faas_sdk.types import File

CELL_ID = UUID("7c93ae23-83ed-4dc9-987a-4676cd3ca6c2")
APP_ID = UUID("6cb3f3a7-8b0d-4f6c-82d9-2c2b61ad1983")


def deployment_body(source):
    if source:
        return DeployCellAppFilesBody(
            source=File(payload=BytesIO(b"source-marker"), file_name="source.tar.gz", mime_type="application/gzip"),
            dockerfile=True,
        )
    return CreateDeploymentRequest(image="image-marker")


def verify_request(request, source):
    assert request.url.path == f"/v1/cells/{CELL_ID}/apps/{APP_ID}/deployments"
    assert request.headers["Authorization"] == "Bearer regional-key"
    assert request.headers["Idempotency-Key"] == "regional-release-1"
    if source:
        content_type = request.headers["Content-Type"]
        boundary = content_type.split("boundary=", 1)[1].strip('"')
        assert boundary != "+++"
        assert f"--{boundary}".encode() in request.content
        assert b'name="source"' in request.content
        assert b"source-marker" in request.content
    else:
        assert b"image-marker" in request.content


@pytest.mark.parametrize("source", [False, True])
def test_cell_deployment_sync_refuses_redirect_with_custom_transport(source):
    requests = []

    def handler(request):
        requests.append(request)
        return httpx.Response(307, headers={"Location": "/recipient"})

    client = AuthenticatedClient(base_url="https://cell.example.com", token="regional-key", follow_redirects=True)
    with httpx.Client(
        base_url="https://cell.example.com",
        headers={"Authorization": "Bearer regional-key"},
        transport=httpx.MockTransport(handler),
        follow_redirects=True,
    ) as transport:
        client.set_httpx_client(transport)
        response = deploy_cell_app.sync_detailed(
            CELL_ID, APP_ID, client=client, body=deployment_body(source), idempotency_key="regional-release-1"
        )
    assert response.status_code == 307
    assert len(requests) == 1
    verify_request(requests[0], source)


@pytest.mark.parametrize("source", [False, True])
def test_cell_deployment_async_refuses_redirect_with_custom_transport(source):
    async def exercise():
        requests = []

        def handler(request):
            requests.append(request)
            return httpx.Response(308, headers={"Location": "/recipient"})

        client = AuthenticatedClient(base_url="https://cell.example.com", token="regional-key", follow_redirects=True)
        async with httpx.AsyncClient(
            base_url="https://cell.example.com",
            headers={"Authorization": "Bearer regional-key"},
            transport=httpx.MockTransport(handler),
            follow_redirects=True,
        ) as transport:
            client.set_async_httpx_client(transport)
            response = await deploy_cell_app.asyncio_detailed(
                CELL_ID, APP_ID, client=client, body=deployment_body(source), idempotency_key="regional-release-1"
            )
        assert response.status_code == 308
        assert len(requests) == 1
        verify_request(requests[0], source)

    asyncio.run(exercise())
