import asyncio

import httpx

from faas_sdk.api.apps import attach_global_app_target
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import AttachGlobalAppTargetRequest


def test_directory_attachment_forces_no_redirect_and_hides_key_repr():
    requests = []

    def handler(request):
        requests.append(request)
        return httpx.Response(307, headers={"Location": "/recipient"})

    body = AttachGlobalAppTargetRequest(regional_app_slug="regional-shop", regional_api_key="regional-key")
    assert "regional-key" not in repr(body)
    client = AuthenticatedClient(base_url="https://directory.example.com", token="directory-key", follow_redirects=True)
    with httpx.Client(
        base_url="https://directory.example.com",
        headers={"Authorization": "Bearer directory-key"},
        transport=httpx.MockTransport(handler),
        follow_redirects=True,
    ) as transport:
        client.set_httpx_client(transport)
        response = attach_global_app_target.sync_detailed("global-shop", "eu-fsn", client=client, body=body)
    assert response.status_code == 307
    assert len(requests) == 1
    assert requests[0].url.path == "/v1/global-apps/global-shop/targets/eu-fsn"
    assert requests[0].headers["Authorization"] == "Bearer directory-key"
    assert b'"regional_api_key":"regional-key"' in requests[0].content


def test_async_directory_attachment_forces_no_redirect():
    async def exercise():
        requests = []

        def handler(request):
            requests.append(request)
            return httpx.Response(307, headers={"Location": "/recipient"})

        client = AuthenticatedClient(
            base_url="https://directory.example.com", token="directory-key", follow_redirects=True
        )
        async with httpx.AsyncClient(
            base_url="https://directory.example.com", transport=httpx.MockTransport(handler), follow_redirects=True
        ) as transport:
            client.set_async_httpx_client(transport)
            response = await attach_global_app_target.asyncio_detailed(
                "global-shop",
                "eu-fsn",
                client=client,
                body=AttachGlobalAppTargetRequest(regional_app_slug="regional-shop", regional_api_key="regional-key"),
            )
        assert response.status_code == 307
        assert len(requests) == 1

    asyncio.run(exercise())
