import asyncio
import base64

import httpx
import pytest

from faas_sdk import (
    DEV_BRIDGE_CONTEXT_HEADER,
    AsyncDevBridgeTransport,
    DevBridgeMiddleware,
    DevBridgeTransport,
    current_dev_bridge_context,
    with_dev_bridge_context,
)


def authority(byte):
    token = base64.urlsafe_b64encode(bytes([byte]) * 32).decode().rstrip("=")
    return f"account.{token}.{token}"


def test_redirect_cannot_leak_context_or_attachment_credentials():
    observed = []

    def upstream(request):
        observed.append((request.url.host, request.headers.get(DEV_BRIDGE_CONTEXT_HEADER)))
        assert "X-Gregale-Dev-Bridge-Token" not in request.headers
        if request.url.host == "payments.svc.gregale":
            assert request.headers["Authorization"] == "Bearer application-auth"
            return httpx.Response(302, headers={"Location": "https://external.example"})
        assert "Authorization" not in request.headers
        return httpx.Response(200)

    with httpx.Client(transport=DevBridgeTransport(httpx.MockTransport(upstream)), follow_redirects=True) as client:
        with with_dev_bridge_context(authority(1)):
            client.get(
                "http://payments.svc.gregale",
                headers={
                    "X-Gregale-Dev-Bridge-Token": "forged",
                    "Authorization": "Bearer application-auth",
                },
            )
    assert observed == [("payments.svc.gregale", authority(1)), ("external.example", None)]
    assert current_dev_bridge_context() is None


@pytest.mark.asyncio
async def test_asgi_requests_and_async_transports_isolate_developers():
    observed = []

    async def upstream(request):
        observed.append(request.headers.get(DEV_BRIDGE_CONTEXT_HEADER))
        return httpx.Response(200)

    async with httpx.AsyncClient(transport=AsyncDevBridgeTransport(httpx.MockTransport(upstream))) as client:

        async def app(scope, receive, send):
            await asyncio.sleep(0)
            await client.get("http://inventory.internal")

        middleware = DevBridgeMiddleware(app)
        await asyncio.gather(
            *[
                middleware(
                    {
                        "type": "http",
                        "headers": [(DEV_BRIDGE_CONTEXT_HEADER.lower().encode(), value.encode())] if value else [],
                    },
                    None,
                    None,
                )
                for value in (authority(1), authority(2), None)
            ]
        )
    assert sorted(value or "ordinary" for value in observed) == sorted([authority(1), authority(2), "ordinary"])
    assert current_dev_bridge_context() is None
