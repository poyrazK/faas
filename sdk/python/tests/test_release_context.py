import asyncio

import httpx

from faas_sdk import (
    GREGALE_RELEASE_HEADER,
    GREGALE_REVISION_HEADER,
    AsyncGregaleReleaseTransport,
    GregaleReleaseMiddleware,
    GregaleReleaseTransport,
    current_gregale_release,
    with_gregale_release,
)


def test_sync_transport_propagates_release_only_to_managed_service():
    seen = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(204)

    client = httpx.Client(transport=GregaleReleaseTransport(httpx.MockTransport(handler)))
    try:
        with with_gregale_release("release-182"):
            client.get(
                "http://billing.svc.gregale:10080/charge",
                headers={GREGALE_REVISION_HEADER: "api-deployment"},
            )
            client.get(
                "https://payments.example.test/charge",
                headers={GREGALE_REVISION_HEADER: "client-session-pin"},
            )
    finally:
        client.close()

    assert seen[0].headers[GREGALE_RELEASE_HEADER] == "release-182"
    assert GREGALE_REVISION_HEADER not in seen[0].headers
    assert GREGALE_RELEASE_HEADER not in seen[1].headers
    assert seen[1].headers[GREGALE_REVISION_HEADER] == "client-session-pin"


def test_explicit_release_wins_and_ambiguous_context_is_ignored():
    seen = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(204)

    client = httpx.Client(transport=GregaleReleaseTransport(httpx.MockTransport(handler)))
    try:
        with with_gregale_release("ambient-release"):
            client.get(
                "http://billing.svc.gregale/charge",
                headers={GREGALE_RELEASE_HEADER: "explicit-release"},
            )
        with with_gregale_release("release-a,release-b"):
            assert current_gregale_release() is None
            client.get("http://billing.svc.gregale/charge")
    finally:
        client.close()

    assert seen[0].headers[GREGALE_RELEASE_HEADER] == "explicit-release"
    assert GREGALE_RELEASE_HEADER not in seen[1].headers


def test_asgi_middleware_captures_request_release_for_async_calls():
    seen = []

    class AsyncHandlerTransport(httpx.AsyncBaseTransport):
        async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
            seen.append(request)
            return httpx.Response(204)

        async def aclose(self) -> None:
            return None

    async def app(scope, receive, send):
        assert current_gregale_release() == "release-182"
        async with httpx.AsyncClient(transport=AsyncGregaleReleaseTransport(AsyncHandlerTransport())) as client:
            await client.get(
                "http://identity.svc.gregale/whoami",
                headers={GREGALE_REVISION_HEADER: "api-deployment"},
            )

    middleware = GregaleReleaseMiddleware(app)
    scope = {
        "type": "http",
        "headers": [(b"x-gregale-release", b"release-182")],
    }
    asyncio.run(middleware(scope, lambda: None, lambda _message: None))

    assert seen[0].headers[GREGALE_RELEASE_HEADER] == "release-182"
    assert GREGALE_REVISION_HEADER not in seen[0].headers
    assert current_gregale_release() is None
