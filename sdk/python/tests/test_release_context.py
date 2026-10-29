import asyncio

import httpx

from faas_sdk import (
    GREGALE_RELEASE_HEADER,
    GREGALE_REQUEST_DEADLINE_HEADER,
    GREGALE_REVISION_HEADER,
    AsyncGregaleReleaseTransport,
    GregaleReleaseMiddleware,
    GregaleReleaseTransport,
    current_gregale_release,
    current_gregale_request_deadline,
    with_gregale_release,
    with_gregale_request_context,
)


def test_sync_deadline_context_wins_and_never_escapes_managed_hosts():
    seen = []
    client = httpx.Client(
        transport=GregaleReleaseTransport(
            httpx.MockTransport(lambda request: seen.append(request) or httpx.Response(204))
        )
    )
    deadline = "v1.key.root.signature"
    try:
        with with_gregale_request_context({GREGALE_REQUEST_DEADLINE_HEADER: deadline}):
            assert current_gregale_request_deadline() == deadline
            client.get(
                "http://billing.svc.gregale/", headers={GREGALE_REQUEST_DEADLINE_HEADER: "v1.key.override.signature"}
            )
            client.get("http://billing.internal/")
            client.get(
                "https://payments.example.test/",
                headers={GREGALE_REQUEST_DEADLINE_HEADER: deadline, "X-Customer": "preserved"},
            )
    finally:
        client.close()
    assert seen[0].headers[GREGALE_REQUEST_DEADLINE_HEADER] == deadline
    assert seen[1].headers[GREGALE_REQUEST_DEADLINE_HEADER] == deadline
    assert GREGALE_REQUEST_DEADLINE_HEADER not in seen[2].headers
    assert seen[2].headers["X-Customer"] == "preserved"
    assert current_gregale_request_deadline() is None


def test_asgi_deadlines_are_isolated_and_reset_after_handler_failure():
    seen = []

    async def app(scope, receive, send):
        deadline = current_gregale_request_deadline()
        await asyncio.sleep(0.01 if "first" in (deadline or "") else 0)
        async with httpx.AsyncClient(
            transport=AsyncGregaleReleaseTransport(
                httpx.MockTransport(
                    lambda request: (
                        seen.append(request.headers.get(GREGALE_REQUEST_DEADLINE_HEADER)) or httpx.Response(204)
                    )
                )
            )
        ) as client:
            await client.get("http://identity.svc.gregale/")
        if scope.get("fail"):
            raise RuntimeError("handler failed")

    middleware = GregaleReleaseMiddleware(app)

    async def run():
        scopes = [
            {"type": "http", "headers": [(GREGALE_REQUEST_DEADLINE_HEADER.lower().encode(), value.encode())]}
            for value in ("v1.key.first.signature", "v1.key.second.signature")
        ]
        await asyncio.gather(*(middleware(scope, None, None) for scope in scopes))
        try:
            await middleware({**scopes[0], "fail": True}, None, None)
        except RuntimeError:
            pass
        assert current_gregale_request_deadline() is None

    asyncio.run(run())
    assert sorted(seen[:2]) == ["v1.key.first.signature", "v1.key.second.signature"]


def test_httpx_redirect_removes_deadline_at_external_destination():
    seen = []

    def handler(request):
        seen.append(request)
        if request.url.host == "billing.svc.gregale":
            return httpx.Response(302, headers={"Location": "https://payments.example.test/"})
        return httpx.Response(204)

    with httpx.Client(transport=GregaleReleaseTransport(httpx.MockTransport(handler)), follow_redirects=True) as client:
        with with_gregale_request_context({GREGALE_REQUEST_DEADLINE_HEADER: "v1.key.root.signature"}):
            response = client.get("http://billing.svc.gregale/")
    assert response.status_code == 204
    assert seen[0].headers[GREGALE_REQUEST_DEADLINE_HEADER] == "v1.key.root.signature"
    assert GREGALE_REQUEST_DEADLINE_HEADER not in seen[1].headers


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
