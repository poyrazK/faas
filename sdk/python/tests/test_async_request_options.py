from __future__ import annotations

import asyncio
import json
import math
from dataclasses import FrozenInstanceError, asdict
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import FaaSClient as PreviewClient
from faas_sdk.preview.api.deployments import get_build as preview_get_build

from faas_sdk import (
    ErrCapacity,
    ErrNotFound,
    FaaSClient,
    FaaSClientOptions,
    RequestOptions,
    RequestTimeoutError,
    RetryOptions,
    RetryPolicy,
)
from faas_sdk.api.deployments import get_build

_SDK = Path(__file__).resolve().parents[1]
_CONTRACT = _SDK / "request-contracts/async-options.json"
if not _CONTRACT.exists():  # Monorepo fixture; the public source mirror maps it beside tests/.
    _CONTRACT = _SDK.parent / "request-contracts/async-options.json"
CONTRACT = json.loads(_CONTRACT.read_text())


class BlockedBody(httpx.AsyncByteStream):
    def __init__(self):
        self.closed = 0
        self.reading = asyncio.Event()

    async def __aiter__(self):
        yield b'{"id":'
        self.reading.set()
        await asyncio.Event().wait()

    async def aclose(self):
        self.closed += 1


def client_for(handler, *, factory=FaaSClient, observer=None, retry=None, token="token"):
    return factory(
        base_url="https://sdk.test/prefix",
        token=token,
        options=FaaSClientOptions(on_response=observer, retry=retry or RetryOptions()),
        httpx_args={"transport": httpx.MockTransport(handler)},
    )


def row(id="build"):
    return {
        "id": id,
        "deployment_id": "deployment",
        "kind": "tarball",
        "source_bytes": 1,
        "status": "succeeded",
        "enqueued_at": "2026-11-14T00:00:00Z",
    }


async def close(client):
    await client.aclose()
    client.close()


@pytest.mark.parametrize("case", CONTRACT["cases"], ids=lambda case: case["name"])
async def test_shared_node_python_contract(case):
    assert CONTRACT["version"] == 1
    reports = []
    keys = []
    calls = 0
    entered = asyncio.Event()
    body = BlockedBody()
    network = OSError("network failed")

    async def handler(request):
        nonlocal calls
        calls += 1
        keys.append(request.headers.get("Idempotency-Key"))
        entered.set()
        if case.get("phase") == "headers":
            await asyncio.Event().wait()
        if case.get("phase") == "network":
            raise network
        response = case["responses"][min(calls - 1, len(case["responses"]) - 1)]
        headers = {"Content-Type": "application/json"}
        if response.get("request_id"):
            headers["X-Request-ID"] = response["request_id"]
        if response.get("retry_after"):
            headers["Retry-After"] = response["retry_after"]
        if case.get("phase") == "body":
            return httpx.Response(200, headers=headers, stream=body)
        value = (
            {"id": case["name"]}
            if response["status"] < 400
            else {
                "code": "not_found" if response["status"] == 404 else "capacity_unavailable",
                "tx_id": "original-tx",
                "status": response["status"],
            }
        )
        return httpx.Response(response["status"], headers=headers, json=value)

    client = client_for(handler)
    attempts = case.get("max_attempts", 1)
    options = RequestOptions(
        timeout=case.get("timeout_ms", 2000) / 1000,
        retry=False if attempts == 1 else RetryPolicy(attempts, 0.001),
        on_response=reports.append,
    )
    try:
        operation = client.arequest(
            lambda: client.async_httpx_client.request(
                case.get("method", "GET"), "/v1/apps/case", json={"slug": "case"}
            ),
            options=options,
        )
        if case["outcome"] == "cancelled":
            task = asyncio.create_task(operation)
            await entered.wait()
            task.cancel("caller reason")
            with pytest.raises(asyncio.CancelledError) as error:
                await task
            assert error.value.args == ("caller reason",)
        elif case["outcome"] == "timeout":
            with pytest.raises(RequestTimeoutError):
                await operation
        elif case["outcome"] in ("capacity", "not_found"):
            with pytest.raises(ErrCapacity if case["outcome"] == "capacity" else ErrNotFound) as error:
                await operation
            assert error.value.problem.tx_id == "original-tx"
        elif case["outcome"] == "network":
            with pytest.raises(OSError) as error:
                await operation
            assert error.value is network
        else:
            assert (await operation).json()["id"] == case["name"]
        assert calls == case["calls"]
        assert [info.status for info in reports] == case["statuses"]
        assert [info.attempt for info in reports] == case["attempts"]
        assert [info.request_id for info in reports] == case["ids"]
        for info in reports:
            assert set(asdict(info)) == {"status", "attempt", "request_id", "duration_ms"}
            assert math.isfinite(info.duration_ms) and info.duration_ms >= 0
            with pytest.raises(FrozenInstanceError):
                info.status = 0
        if case.get("stable_key"):
            assert len(set(keys)) == 1 and keys[0]
        if "closed" in case:
            assert body.closed == case["closed"]
    finally:
        await close(client)


@pytest.mark.parametrize("factory,service", [(FaaSClient, get_build), (PreviewClient, preview_get_build)])
async def test_generated_async_results_and_existing_sync_callers_remain_usable(factory, service):
    def handler(request):
        return httpx.Response(200, json=row())

    client = client_for(handler, factory=factory)
    try:
        result = await client.arequest(
            lambda: service.asyncio("build", client=client.inner), options=RequestOptions(timeout=1)
        )
        assert result.id == "build" and result.status == "succeeded"
        assert service.sync("build", client=client.inner).id == "build"
        assert (await service.asyncio("build", client=client.inner)).id == "build"
    finally:
        await close(client)


async def test_concurrent_client_and_task_policies_do_not_mutate_shared_httpx_configuration():
    counts = {}
    reports = {}
    seen_tokens = {}

    async def handler(request):
        id = request.url.path.split("/")[-1]
        counts[id] = counts.get(id, 0) + 1
        seen_tokens[id] = request.headers["Authorization"]
        await asyncio.sleep(0)
        if id.endswith("retry") and counts[id] == 1:
            return httpx.Response(503, json={"code": "capacity_unavailable"})
        return httpx.Response(200, json=row(id))

    clients = [client_for(handler, token="alice"), client_for(handler, token="bob")]
    original = [(client.async_httpx_client.timeout, dict(client.async_httpx_client.headers)) for client in clients]

    async def run(index):
        client = clients[index % 2]
        id = f"task-{index}-{'retry' if index % 2 else 'once'}"
        own = []
        reports[id] = own
        result = await client.arequest(
            lambda: get_build.asyncio(id, client=client.inner),
            options=RequestOptions(
                timeout=1, retry=RetryPolicy(2, 0.001) if index % 2 else False, on_response=own.append
            ),
        )
        assert result.id == id
        assert counts[id] == (2 if index % 2 else 1)
        assert seen_tokens[id] == ("Bearer bob" if index % 2 else "Bearer alice")
        assert [info.attempt for info in own] == ([1, 2] if index % 2 else [1])

    try:
        await asyncio.gather(*(run(i) for i in range(40)))
        assert len(reports) == 40
        for client, (timeout, headers) in zip(clients, original, strict=True):
            assert client.async_httpx_client.timeout == timeout
            assert dict(client.async_httpx_client.headers) == headers
    finally:
        for client in clients:
            await close(client)


async def test_independent_deadlines_and_zero_override_leave_sibling_running():
    release = asyncio.Event()

    async def handler(request):
        await release.wait()
        return httpx.Response(200, json=row(request.url.path.split("/")[-1]))

    client = client_for(handler)
    client._request_timeout = 0.02
    try:
        short = asyncio.create_task(client.arequest(lambda: get_build.asyncio("short", client=client.inner)))
        long = asyncio.create_task(
            client.arequest(lambda: get_build.asyncio("long", client=client.inner), options=RequestOptions(timeout=0))
        )
        with pytest.raises(RequestTimeoutError):
            await short
        assert not long.done()
        release.set()
        assert (await long).id == "long"
    finally:
        await close(client)


async def test_async_observer_failures_are_nonblocking_and_both_observers_get_same_info():
    client_reports = []
    own_reports = []
    entered = asyncio.Event()

    async def observer(info):
        client_reports.append(info)
        entered.set()
        raise RuntimeError("observer failed")

    def own(info):
        own_reports.append(info)
        raise ValueError("observer failed")

    client = client_for(lambda request: httpx.Response(200, json=row()), observer=observer)
    try:
        result = await client.arequest(
            lambda: get_build.asyncio("build", client=client.inner), options=RequestOptions(on_response=own)
        )
        await entered.wait()
        await asyncio.sleep(0)
        assert result.id == "build" and own_reports[0] is client_reports[0]
        # Unscoped generated calls also use the client-wide async observer.
        await get_build.asyncio("build", client=client.inner)
        await asyncio.sleep(0)
        assert len(client_reports) == 2
    finally:
        await close(client)


async def test_cancellation_closes_body_and_retry_wait_without_changing_cancelled_error():
    body = BlockedBody()
    client = client_for(lambda request: httpx.Response(200, stream=body))
    try:
        task = asyncio.create_task(client.arequest(lambda: client.async_httpx_client.get("/body")))
        await body.reading.wait()
        task.cancel("stop body")
        with pytest.raises(asyncio.CancelledError) as error:
            await task
        assert error.value.args == ("stop body",)
        assert body.closed == 1
    finally:
        await close(client)


async def test_late_transport_response_after_timeout_is_closed_without_observer_delivery():
    release = asyncio.Event()
    reports = []
    body = BlockedBody()

    async def handler(request):
        try:
            await asyncio.Event().wait()
        except asyncio.CancelledError:
            await release.wait()
            return httpx.Response(200, stream=body)

    client = client_for(handler, observer=reports.append)
    try:
        with pytest.raises(RequestTimeoutError):
            await client.arequest(lambda: client.async_httpx_client.get("/late"), options=RequestOptions(timeout=0.02))
        release.set()
        for _ in range(4):
            await asyncio.sleep(0)
        assert body.closed == 1 and reports == []
    finally:
        release.set()
        await close(client)


def test_options_are_immutable_and_validated():
    options = RequestOptions(timeout=1, retry=RetryPolicy(2))
    with pytest.raises(FrozenInstanceError):
        options.timeout = 2
    with pytest.raises(FrozenInstanceError):
        options.retry.max_attempts = 1
    for value in [-1, float("nan"), float("inf"), True, "bad"]:
        with pytest.raises((ValueError, TypeError)):
            RequestOptions(timeout=value)
    for value in [0, 10, 1.5, True]:
        with pytest.raises(ValueError):
            RetryPolicy(max_attempts=value)
    with pytest.raises(TypeError):
        RequestOptions(retry=True)


async def test_real_http_concurrency_keeps_accounts_and_diagnostics_with_their_task():
    requests = []

    async def serve(reader, writer):
        try:
            headers = (await reader.readuntil(b"\r\n\r\n")).decode()
            path = headers.split(" ", 2)[1]
            id = path.split("/")[-1]
            index = int(id.split("-")[-1])
            assert f"Authorization: Bearer {'bob' if index % 2 else 'alice'}" in headers
            requests.append(id)
            await asyncio.sleep(0)
            body = json.dumps(row(id)).encode()
            writer.write(
                f"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {len(body)}\r\nX-Request-ID: 11111111-1111-4111-8111-{index:012}\r\nConnection: close\r\n\r\n".encode()
                + body
            )
            await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()

    server = await asyncio.start_server(serve, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    clients = [FaaSClient(base_url=f"http://127.0.0.1:{port}", token=token) for token in ("alice", "bob")]

    async def run(index):
        client = clients[index % 2]
        reports = []
        result = await client.arequest(
            lambda: get_build.asyncio(f"task-{index}", client=client.inner),
            options=RequestOptions(timeout=5, on_response=reports.append),
        )
        assert result.id == f"task-{index}"
        assert len(reports) == 1 and reports[0].request_id.endswith(f"{index:012}")

    try:
        await asyncio.gather(*(run(i) for i in range(40)))
        assert len(requests) == 40
    finally:
        for client in clients:
            await close(client)
        server.close()
        await server.wait_closed()


async def test_factory_must_not_reuse_an_already_started_task():
    client = client_for(lambda request: httpx.Response(200, json=row()))
    task = asyncio.create_task(asyncio.sleep(0))
    try:
        with pytest.raises(TypeError, match="fresh coroutine"):
            await client.arequest(lambda: task)
        await task
    finally:
        await close(client)


async def test_total_deadline_bounds_async_auth_before_transport_starts():
    calls = 0

    class SlowAuth(httpx.Auth):
        async def async_auth_flow(self, request):
            await asyncio.Event().wait()
            yield request

    def handler(request):
        nonlocal calls
        calls += 1
        return httpx.Response(200, json=row())

    client = client_for(handler)
    auth = SlowAuth()
    client.async_httpx_client.auth = auth
    try:
        with pytest.raises(RequestTimeoutError):
            await client.arequest(
                lambda: get_build.asyncio("build", client=client.inner), options=RequestOptions(timeout=0.02)
            )
        assert calls == 0 and client.async_httpx_client.auth is auth
    finally:
        await close(client)


async def test_pending_observers_do_not_delay_requests_and_close_cancels_their_task():
    entered = asyncio.Event()
    cancelled = asyncio.Event()

    async def observer(info):
        entered.set()
        try:
            await asyncio.Event().wait()
        finally:
            cancelled.set()

    client = client_for(lambda request: httpx.Response(200, json=row()), observer=observer)
    try:
        assert (
            await client.arequest(
                lambda: get_build.asyncio("build", client=client.inner), options=RequestOptions(timeout=1)
            )
        ).id == "build"
        await entered.wait()
        assert not cancelled.is_set()
    finally:
        await close(client)
    assert cancelled.is_set()


async def test_observer_requests_do_not_inherit_a_completed_request_scope():
    release = asyncio.Event()
    done = asyncio.Event()
    own = []
    calls = 0
    observed = 0

    async def observer(info):
        nonlocal observed
        observed += 1
        if observed == 1:
            await release.wait()
            await client.async_httpx_client.get("/observer")
            done.set()

    def handler(request):
        nonlocal calls
        calls += 1
        return httpx.Response(200, json=row())

    client = client_for(handler, observer=observer)
    try:
        await client.arequest(
            lambda: get_build.asyncio("build", client=client.inner), options=RequestOptions(on_response=own.append)
        )
        release.set()
        await asyncio.wait_for(done.wait(), 1)
        await asyncio.sleep(0)
        assert calls == 2 and observed == 2 and len(own) == 1
    finally:
        await close(client)


async def test_legacy_constructor_retry_count_is_preserved_and_call_override_stays_local():
    counts = {}

    async def handler(request):
        id = request.url.path.split("/")[-1]
        counts[id] = counts.get(id, 0) + 1
        return (
            httpx.Response(503, json={"code": "capacity_unavailable"})
            if counts[id] < 3
            else httpx.Response(200, json=row(id))
        )

    client = client_for(handler, retry=RetryOptions(max_attempts=2, backoff=0.001))
    try:
        assert (await client.arequest(lambda: get_build.asyncio("default", client=client.inner))).id == "default"
        with pytest.raises(ErrCapacity):
            await client.arequest(
                lambda: get_build.asyncio("once", client=client.inner), options=RequestOptions(retry=False)
            )
        assert counts == {"default": 3, "once": 1}
    finally:
        await close(client)


async def test_a_scoped_operation_does_not_apply_its_policy_or_observer_to_another_client():
    own = []
    other = []
    client = client_for(lambda request: httpx.Response(200, json=row("first")))
    sibling = client_for(lambda request: httpx.Response(200, json=row("second")), observer=other.append)

    async def operation():
        first = await get_build.asyncio("first", client=client.inner)
        second = await get_build.asyncio("second", client=sibling.inner)
        return first, second

    try:
        first, second = await client.arequest(operation, options=RequestOptions(on_response=own.append))
        assert (first.id, second.id) == ("first", "second")
        assert len(own) == len(other) == 1
    finally:
        await close(client)
        await close(sibling)


async def test_a_one_shot_async_upload_is_never_replayed():
    calls = 0
    chunks = 0

    async def content():
        nonlocal chunks
        chunks += 1
        yield b"upload"

    def handler(request):
        nonlocal calls
        calls += 1
        assert request.content == b"upload"
        return httpx.Response(503, json={"code": "capacity_unavailable"})

    client = client_for(handler)
    try:
        with pytest.raises(ErrCapacity):
            await client.arequest(
                lambda: client.async_httpx_client.post("/upload", content=content()),
                options=RequestOptions(retry=RetryPolicy(3, 0.001)),
            )
        assert calls == chunks == 1
    finally:
        await close(client)


async def test_a_disabled_constructor_timeout_is_inherited_without_changing_httpx_settings():
    async def handler(request):
        assert set(request.extensions["timeout"].values()) == {None}
        return httpx.Response(200, json=row())

    client = FaaSClient(
        base_url="https://sdk.test", timeout=None, httpx_args={"transport": httpx.MockTransport(handler)}
    )
    try:
        before = client.async_httpx_client.timeout
        assert (await client.arequest(lambda: get_build.asyncio("build", client=client.inner))).id == "build"
        assert client.async_httpx_client.timeout == before
    finally:
        await close(client)


@pytest.mark.parametrize("action", ["timeout", "cancel"])
async def test_real_http_blocked_body_releases_the_connection_on_stop(action):
    disconnected = asyncio.Event()
    received = asyncio.Event()

    async def serve(reader, writer):
        try:
            await reader.readuntil(b"\r\n\r\n")
            writer.write(b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 10000\r\n\r\n{"id":')
            await writer.drain()
            await reader.read()
            disconnected.set()
        finally:
            writer.close()
            await writer.wait_closed()

    server = await asyncio.start_server(serve, "127.0.0.1", 0)
    client = FaaSClient(base_url=f"http://127.0.0.1:{server.sockets[0].getsockname()[1]}")
    try:
        task = asyncio.create_task(
            client.arequest(
                lambda: client.async_httpx_client.get("/slow"),
                options=RequestOptions(
                    timeout=0.1 if action == "timeout" else 2, on_response=lambda info: received.set()
                ),
            )
        )
        await received.wait()
        if action == "cancel":
            task.cancel("stop socket")
            with pytest.raises(asyncio.CancelledError) as error:
                await task
            assert error.value.args == ("stop socket",)
        else:
            with pytest.raises(RequestTimeoutError):
                await task
        # Check before closing the client: stopping the request must release its socket.
        await asyncio.wait_for(disconnected.wait(), 2)
    finally:
        await close(client)
        server.close()
        await server.wait_closed()
