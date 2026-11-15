"""Typed pagination behavior, including the fixtures also run by Node."""

from __future__ import annotations

import asyncio
import datetime
import importlib.util
import json
from dataclasses import FrozenInstanceError
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import FaaSClient as PreviewClient
from faas_sdk.preview.api.deployments import list_app_deployments as preview_list_app
from faas_sdk.preview.api.deployments import list_deployments as preview_list_account

from faas_sdk import (
    ErrNotFound,
    FaaSClient,
    FaaSClientOptions,
    PaginationError,
    PaginationOptions,
    PaginationPage,
    RequestTimeoutError,
    RetryOptions,
    RetryPolicy,
)
from faas_sdk.api.deployments import list_app_deployments, list_deployments
from faas_sdk.models.build_response import BuildResponse
from faas_sdk.models.deployment_list_response import DeploymentListResponse
from faas_sdk.models.deployment_response import DeploymentResponse

SDK = Path(__file__).resolve().parents[1]
CONTRACT_PATH = SDK / "request-contracts/pagination.json"
if not CONTRACT_PATH.exists():
    CONTRACT_PATH = SDK.parent / "request-contracts/pagination.json"
CONTRACT = json.loads(CONTRACT_PATH.read_text())
CURSOR = "2026-11-14T00:00:00.123456789Z|opaque/+%?"


def row(id="row"):
    return dict(
        id=id,
        app_id="app",
        image_digest="sha256:abc",
        kind="tarball",
        status="succeeded",
        created_at="2026-11-14T00:00:00Z",
        deployment_id="deployment",
        source_bytes=1,
        enqueued_at="2026-11-14T00:00:00Z",
    )


def client_for(handler, *, factory=FaaSClient, **kwargs):
    return factory(
        base_url="https://sdk.test/prefix",
        token="before",
        httpx_args={"transport": httpx.MockTransport(handler)},
        **kwargs,
    )


async def close(client):
    await client.aclose()
    client.close()


class BlockedBody(httpx.AsyncByteStream):
    def __init__(self):
        self.reading = asyncio.Event()
        self.closed = 0

    async def __aiter__(self):
        yield b'{"items":['
        self.reading.set()
        await asyncio.Event().wait()

    async def aclose(self):
        self.closed += 1


@pytest.mark.parametrize("factory", [FaaSClient, PreviewClient], ids=["public", "preview"])
@pytest.mark.parametrize("mode", ["items", "pages"])
@pytest.mark.parametrize("case", CONTRACT["cases"], ids=lambda case: case["name"])
async def test_shared_node_python_pagination(case, factory, mode):
    assert CONTRACT["version"] == 1
    urls, ids = [], []
    entered = asyncio.Event()
    body = BlockedBody()

    async def handler(request):
        urls.append(request.url)
        entered.set()
        route = {"app": "/v1/apps/my%2Fapp/deployments", "account": "/v1/deployments", "builds": "/v1/builds"}[
            case["route"]
        ]
        assert request.url.raw_path.split(b"?")[0] == ("/prefix" + route).encode()
        if case["route"] == "builds":
            assert request.url.params.get("app") == case.get("app")
            assert request.url.params.get("status") == case.get("status")
        phase = case.get("phase")
        if phase == "headers":
            await asyncio.Event().wait()
        if phase == "body":
            return httpx.Response(200, stream=body)
        if phase == "retry":
            return httpx.Response(503, json={"code": "capacity_unavailable"}, headers={"Retry-After": "60"})
        if phase == "not_found":
            return httpx.Response(404, json={"code": "not_found", "tx_id": "original-tx", "status": 404})
        page = case["pages"][len(urls) - 1]
        return httpx.Response(
            200,
            json={
                "items": [row(id) for id in page["ids"]],
                **({"next_before": page["next_before"]} if "next_before" in page else {}),
            },
        )

    client = client_for(handler, factory=factory)
    options = PaginationOptions(
        timeout=case.get("timeout_ms", 2000) / 1000,
        max_pages=case.get("max_pages"),
        max_items=case.get("max_items"),
        retry=RetryPolicy(case["max_attempts"], 0.001) if case.get("max_attempts") else False,
    )
    params = dict(limit=case.get("limit", 50), before=case.get("before"), options=options)
    if case["route"] == "app":
        iterator = (client.aiter_app_deployments if mode == "items" else client.aiter_app_deployment_pages)(
            "my/app", **params
        )
    elif case["route"] == "account":
        iterator = (client.aiter_deployments if mode == "items" else client.aiter_deployment_pages)(**params)
    else:
        filters = {key: case[key] for key in ("app", "status") if key in case}
        iterator = (client.aiter_builds if mode == "items" else client.aiter_build_pages)(**filters, **params)
    assert not urls

    async def consume():
        async with iterator:
            async for value in iterator:
                ids.extend(item.id for item in value.items) if mode == "pages" else ids.append(value.id)

    try:
        pending = asyncio.create_task(consume())
        outcome = case.get("outcome")
        if outcome == "cancelled":
            await asyncio.wait_for(entered.wait(), 1)
            await asyncio.wait_for(iterator.aclose(), 1)
            with pytest.raises(asyncio.CancelledError):
                await pending
        elif outcome == "timeout":
            with pytest.raises(RequestTimeoutError):
                await asyncio.wait_for(pending, 1)
        elif outcome == "not_found":
            with pytest.raises(ErrNotFound) as error:
                await pending
            assert error.value.tx_id == "original-tx"
        elif outcome:
            with pytest.raises(PaginationError) as error:
                await pending
            assert error.value.code == outcome
            assert "private-cursor" not in str(error.value)
        else:
            await pending
        await asyncio.sleep(0)
        assert ids == case["ids"]
        assert len(urls) == case["calls"]
        if "cursors" in case:
            assert [url.params.get("before") for url in urls] == case["cursors"]
        if "limits" in case:
            assert [int(url.params["limit"]) for url in urls] == case["limits"]
        if "closed" in case:
            assert body.closed == case["closed"]
        with pytest.raises(StopAsyncIteration):
            await anext(iterator)
    finally:
        await iterator.aclose()
        await close(client)


@pytest.mark.parametrize("module", [list_app_deployments, list_deployments, preview_list_app, preview_list_account])
def test_generated_calls_accept_lossless_strings_and_legacy_datetimes(module):
    args = ("api",) if "list_app_" in module.__name__ else ()
    precise = "2026-11-14T00:00:00.123456789Z"
    assert module._get_kwargs(*args, before=precise)["params"]["before"] == precise
    legacy = datetime.datetime(2026, 11, 14, tzinfo=datetime.UTC)
    assert module._get_kwargs(*args, before=legacy)["params"]["before"] == legacy.isoformat()
    assert "before" not in module._get_kwargs(*args)["params"]
    # The existing generated response shape and datetime result stay compatible.
    page = DeploymentListResponse.from_dict({"items": [], "next_before": precise})
    assert isinstance(page.next_before, datetime.datetime)


def test_cursor_generator_adaptation_is_idempotent_and_fails_closed(tmp_path):
    spec = importlib.util.spec_from_file_location("sdk_gen", SDK / "scripts/gen.py")
    generator = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(generator)
    for prefix in ("", "preview/"):
        for name in ("list_app_deployments.py", "list_deployments.py"):
            source = SDK / "faas_sdk" / prefix / "api/deployments" / name
            target = tmp_path / prefix / "api/deployments" / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(source.read_bytes())
    generator._preserve_pagination_cursor_inputs(tmp_path)
    first = {p: p.read_bytes() for p in tmp_path.rglob("*.py")}
    generator._preserve_pagination_cursor_inputs(tmp_path)
    assert first == {p: p.read_bytes() for p in first}
    path = next(iter(first))
    path.write_text(path.read_text().replace("json_before = before.isoformat()", "json_before = str(before)"))
    with pytest.raises(ValueError, match="needs review"):
        generator._preserve_pagination_cursor_inputs(tmp_path)


@pytest.mark.parametrize("key", ["max_pages", "max_items"])
@pytest.mark.parametrize("value", [-1, 0.5, float("nan"), float("inf"), 2**53, True])
def test_invalid_limits(key, value):
    with pytest.raises(ValueError):
        PaginationOptions(**{key: value})


@pytest.mark.parametrize("value", [0, 201, 0.5, True])
async def test_invalid_page_sizes_do_no_io(value):
    def handler(request):
        pytest.fail("invalid options must not perform HTTP")

    client = client_for(handler)
    try:
        with pytest.raises(ValueError):
            client.aiter_deployments(limit=value)
        with pytest.raises(TypeError):
            client.aiter_builds(status="cancelled")
        with pytest.raises(TypeError):
            client.aiter_builds(before=datetime.datetime.now())
    finally:
        await close(client)


async def test_pages_are_typed_immutable_and_keep_exact_resumable_boundary():
    calls = []

    def handler(request):
        calls.append(request.url)
        return httpx.Response(200, json={"items": [row()], "next_before": CURSOR if len(calls) == 1 else None})

    client = client_for(handler)
    try:
        async with client.aiter_app_deployment_pages("api", options=PaginationOptions(max_pages=1)) as pages:
            page = await anext(pages)
            assert isinstance(page, PaginationPage)
            assert isinstance(page.items[0], DeploymentResponse)
            assert page.next_before == CURSOR
            with pytest.raises(FrozenInstanceError):
                page.next_before = "changed"
        async with client.aiter_builds(before=page.next_before, options=PaginationOptions(max_items=1)) as items:
            assert isinstance(await anext(items), BuildResponse)
        assert calls[1].params["before"] == CURSOR
        assert calls[1].params["limit"] == "1"
        assert len(calls) == 2
    finally:
        await close(client)


async def test_configuration_is_captured_and_close_does_not_close_shared_transport():
    seen, closed = [], []

    class Transport(httpx.AsyncBaseTransport):
        async def handle_async_request(self, request):
            seen.append((str(request.url), request.headers["Authorization"], request.headers.get("X-Context")))
            return httpx.Response(200, json={"items": [row()], "next_before": f"cursor-{len(seen)}"})

        async def aclose(self):
            closed.append(True)

    client = FaaSClient(base_url="https://before.test/prefix", token="before", httpx_args={"transport": Transport()})
    original = client.async_httpx_client
    original.headers["X-Context"] = "first"
    iterator = client.aiter_builds(app="api", options=PaginationOptions(max_pages=2))
    original.headers["Authorization"] = "Bearer after"
    original.headers["X-Context"] = "second"
    original.base_url = "https://after.test/prefix"
    try:
        assert len([item async for item in iterator]) == 2
        assert not closed
        assert all(
            url.startswith("https://before.test/prefix/") and auth == "Bearer before" and context == "first"
            for url, auth, context in seen
        )
        assert all("app=api" in url for url, _, _ in seen)
        async with client.aiter_build_pages(options=PaginationOptions(max_pages=1)) as pages:
            await anext(pages)
        assert seen[-1][0].startswith("https://after.test/prefix/")
        assert seen[-1][1:] == ("Bearer after", "second")
        assert not closed and client.async_httpx_client is original
    finally:
        await close(client)
    assert len(closed) == 1


async def test_deadline_includes_consumer_pauses_and_buffered_items():
    calls = []

    def handler(request):
        calls.append(request)
        return httpx.Response(200, json={"items": [row("a"), row("b")], "next_before": CURSOR})

    client = client_for(handler)
    try:
        iterator = client.aiter_deployments(options=PaginationOptions(timeout=0.02))
        await asyncio.sleep(0.03)  # Budget starts on first consumption, not creation.
        assert (await anext(iterator)).id == "a"
        await asyncio.sleep(0.03)
        with pytest.raises(RequestTimeoutError):
            await anext(iterator)
        assert len(calls) == 1
    finally:
        await close(client)


async def test_deadlines_and_retry_policies_are_isolated_between_concurrent_traversals():
    counts, reports = {}, []
    release = asyncio.Event()

    async def handler(request):
        app = request.url.params["app"]
        counts[app] = counts.get(app, 0) + 1
        if app in ("short", "long"):
            await release.wait()
        if app == "retry" and counts[app] == 1:
            return httpx.Response(503, json={"code": "capacity_unavailable"})
        return httpx.Response(200, json={"items": [row(app)]})

    client = client_for(handler, timeout=0.02, options=FaaSClientOptions(retry=RetryOptions(max_attempts=0)))

    async def collect(**kwargs):
        return [item.id async for item in client.aiter_builds(**kwargs)]

    try:
        short = asyncio.create_task(collect(app="short"))
        long = asyncio.create_task(collect(app="long", options=PaginationOptions(timeout=0)))
        retried = asyncio.create_task(
            collect(
                app="retry",
                options=PaginationOptions(timeout=1, retry=RetryPolicy(2, 0.001), on_response=reports.append),
            )
        )
        with pytest.raises(RequestTimeoutError):
            await short
        assert not long.done()
        release.set()
        assert await long == ["long"]
        assert await retried == ["retry"]
        assert counts == {"short": 1, "long": 1, "retry": 2}
        assert [info.attempt for info in reports] == [1, 2]
        assert client.async_httpx_client.timeout == httpx.Timeout(0.02)
    finally:
        release.set()
        await close(client)


@pytest.mark.parametrize("phase", ["headers", "body"])
@pytest.mark.parametrize("action", ["cancel", "close"])
async def test_pending_reads_cancel_and_close_promptly(phase, action):
    entered = asyncio.Event()
    body = BlockedBody()

    async def handler(request):
        entered.set()
        if phase == "headers":
            await asyncio.Event().wait()
        return httpx.Response(200, stream=body)

    client = client_for(handler)
    iterator = client.aiter_builds(options=PaginationOptions(timeout=0))
    try:
        pending = asyncio.create_task(anext(iterator))
        await asyncio.wait_for((body.reading if phase == "body" else entered).wait(), 1)
        if action == "cancel":
            pending.cancel("caller reason")
        else:
            await asyncio.wait_for(iterator.aclose(), 1)
        with pytest.raises(asyncio.CancelledError) as error:
            await asyncio.wait_for(pending, 1)
        if action == "cancel":
            assert error.value.args == ("caller reason",)
        await asyncio.sleep(0)
        if phase == "body":
            assert body.closed == 1
        with pytest.raises(StopAsyncIteration):
            await anext(iterator)
    finally:
        await iterator.aclose()
        await close(client)


async def test_early_exit_close_before_start_and_concurrent_anext_guard():
    calls = []
    entered, release = asyncio.Event(), asyncio.Event()

    async def handler(request):
        calls.append(request)
        entered.set()
        await release.wait()
        return httpx.Response(200, json={"items": [row("a"), row("b")], "next_before": CURSOR})

    client = client_for(handler)
    try:
        idle = client.aiter_builds()
        await idle.aclose()
        assert not calls
        iterator = client.aiter_builds()
        pending = asyncio.create_task(anext(iterator))
        await entered.wait()
        with pytest.raises(RuntimeError, match="one pending"):
            await anext(iterator)
        release.set()
        assert (await pending).id == "a"
        async with iterator:
            assert (await anext(iterator)).id == "b"
        with pytest.raises(StopAsyncIteration):
            await anext(iterator)
        assert len(calls) == 1
    finally:
        release.set()
        await close(client)


@pytest.mark.parametrize("body", [None, {}, {"items": {}}, {"items": [42]}, {"items": [{}]}])
async def test_invalid_envelopes_and_unparseable_rows_fail_without_body_data(body):
    client = client_for(lambda request: httpx.Response(200, json=body))
    try:
        with pytest.raises(PaginationError) as error:
            await anext(client.aiter_deployments())
        assert error.value.code == "invalid_page"
        assert str(error.value) == "Invalid pagination page"
    finally:
        await close(client)


async def test_retry_stays_on_same_page_and_page_cap_counts_pages_not_attempts():
    requests, reports = [], []

    def handler(request):
        requests.append(request.url)
        if len(requests) == 1:
            return httpx.Response(503, json={"code": "capacity_unavailable"})
        return httpx.Response(200, json={"items": [row()], "next_before": CURSOR})

    client = client_for(handler)
    try:
        items = [
            item
            async for item in client.aiter_builds(
                before="seed",
                options=PaginationOptions(max_pages=1, retry=RetryPolicy(2, 0.001), on_response=reports.append),
            )
        ]
        assert len(items) == 1
        assert len(requests) == 2 and requests[0] == requests[1]
        assert [report.attempt for report in reports] == [1, 2]
    finally:
        await close(client)


async def test_native_http_concurrent_accounts_cursors_and_cancellation():
    seen, connections = [], set()
    disconnected = asyncio.Event()
    entered = asyncio.Event()

    async def serve(reader, writer):
        connections.add(writer)
        try:
            data = await reader.readuntil(b"\r\n\r\n")
            lines = data.decode().split("\r\n")
            url = httpx.URL("http://local" + lines[0].split()[1])
            headers = httpx.Headers(line.split(": ", 1) for line in lines[1:] if ": " in line)
            seen.append((headers["Authorization"], dict(url.params)))
            if url.params.get("app") == "blocked":
                entered.set()
                assert await reader.read() == b""  # Caller cancellation closes the real socket.
                disconnected.set()
                return
            await asyncio.sleep(0.001)
            token = headers["Authorization"].removeprefix("Bearer ")
            second = "before" in url.params
            body = json.dumps(
                {"items": [row(token + ("-2" if second else "-1"))], "next_before": None if second else token + CURSOR}
            ).encode()
            writer.write(
                b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: "
                + str(len(body)).encode()
                + b"\r\n\r\n"
                + body
            )
            await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()
            connections.discard(writer)

    server = await asyncio.start_server(serve, "127.0.0.1", 0)
    base_url = f"http://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    alice = FaaSClient(base_url=base_url, token="alice")
    bob = FaaSClient(base_url=base_url, token="bob")

    async def collect(client, app):
        return [
            item.id
            async for item in client.aiter_builds(app=app, limit=1, options=PaginationOptions(timeout=2, retry=False))
        ]

    try:
        blocked = asyncio.create_task(collect(alice, "blocked"))
        await asyncio.wait_for(entered.wait(), 2)
        blocked.cancel("stop socket")
        with pytest.raises(asyncio.CancelledError, match="stop socket"):
            await blocked
        await asyncio.wait_for(disconnected.wait(), 1)
        results = await asyncio.gather(*(collect(alice if i % 2 == 0 else bob, "api") for i in range(12)))
        assert results == [["alice-1", "alice-2"] if i % 2 == 0 else ["bob-1", "bob-2"] for i in range(12)]
        for authorization, params in seen:
            if "before" in params:
                assert params["before"] == authorization.removeprefix("Bearer ") + CURSOR
        assert len(seen) == 25
    finally:
        await close(alice)
        await close(bob)
        server.close()
        await server.wait_closed()
        for writer in tuple(connections):
            writer.close()
        await asyncio.sleep(0)
