"""Read-only waiter behavior, including fixtures shared with the Node SDK."""

from __future__ import annotations

import asyncio
import json
from dataclasses import FrozenInstanceError
from pathlib import Path

import httpx
import pytest

from faas_sdk import (
    BuildWaitOptions,
    DeploymentWaitOptions,
    ErrNotFound,
    ErrUnauthorized,
    FaaSClient,
    FaaSClientOptions,
    RequestTimeoutError,
    RetryOptions,
    RetryPolicy,
    WaiterError,
    WaiterOptions,
    WaiterProgress,
    WaiterTimeoutError,
)
from faas_sdk.models.build_response import BuildResponse
from faas_sdk.models.release_timeline import ReleaseTimeline
from faas_sdk.preview import FaaSClient as PreviewClient

SDK = Path(__file__).resolve().parents[1]
CONTRACT_PATH = SDK / "request-contracts/waiters.json"
if not CONTRACT_PATH.exists():
    CONTRACT_PATH = SDK.parent / "request-contracts/waiters.json"
CONTRACT = json.loads(CONTRACT_PATH.read_text())
DEPLOYMENT_ID = "3f6e0f10-c939-4e10-8e6b-15c1a39807ad"
FAST = dict(timeout=2, min_delay=0.001, max_delay=0.001)


def build(id="build/1", status="succeeded"):
    return dict(
        id=id,
        deployment_id=DEPLOYMENT_ID,
        status=status,
        kind="tarball",
        source_bytes=1,
        enqueued_at="2026-11-14T13:20:00Z",
    )


def release(phase="released"):
    return dict(
        deployment_id=DEPLOYMENT_ID,
        app="api",
        phase=phase,
        status="live",
        traffic_percent=100,
        summary=phase,
        created_at="2026-11-14T13:20:00Z",
        events=[],
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
        self.closed = 0
        self.reading = asyncio.Event()

    async def __aiter__(self):
        yield b'{"id":'
        self.reading.set()
        await asyncio.Event().wait()

    async def aclose(self):
        self.closed += 1


@pytest.mark.parametrize("factory", [FaaSClient, PreviewClient], ids=["public", "preview"])
@pytest.mark.parametrize("case", CONTRACT["cases"], ids=lambda case: case["name"])
async def test_shared_node_python_waiters(case, factory):
    assert CONTRACT["version"] == 1
    calls = 0
    progress, reports = [], []
    entered = asyncio.Event()
    body = BlockedBody()
    callback_error, network = RuntimeError("progress failed"), OSError("network failed")

    async def handler(request):
        nonlocal calls
        calls += 1
        assert request.method == "GET"
        route = (
            "/v1/builds/" + case["id"].replace("/", "%2F")
            if case["resource"] == "build"
            else "/v1/deployments/" + case["id"] + "/timeline"
        )
        assert request.url.raw_path.decode() == "/prefix" + route
        if case.get("phase") != "progress":
            entered.set()
        if case.get("phase") == "headers":
            await asyncio.Event().wait()
        if case.get("phase") == "network":
            raise network
        if case.get("phase") == "body":
            return httpx.Response(200, stream=body)
        response = case["responses"][calls - 1]
        return httpx.Response(
            response["status"],
            json=response["body"],
            headers={"Retry-After": response["retry_after"]} if "retry_after" in response else {},
        )

    async def on_progress(value, info):
        assert isinstance(value, BuildResponse if case["resource"] == "build" else ReleaseTimeline)
        progress.append(value.status if case["resource"] == "build" else value.phase)
        assert isinstance(info, WaiterProgress) and info.poll >= 1 and info.elapsed_ms >= 0
        with pytest.raises(FrozenInstanceError):
            info.poll = 0
        if case.get("phase") == "callback_error":
            raise callback_error
        if case.get("phase") == "progress":
            entered.set()
            await asyncio.Event().wait()

    client = client_for(handler, factory=factory, timeout=0.001)
    common = dict(
        **FAST,
        max_polls=case.get("max_polls"),
        retry=RetryPolicy(case["max_attempts"], 0.001) if case.get("max_attempts") else False,
        on_response=reports.append,
        on_progress=on_progress,
    )
    common["timeout"] = case.get("timeout_ms", 2000) / 1000

    def wait():
        if case["resource"] == "build":
            return client.wait_for_build(case["id"], options=BuildWaitOptions(**common))
        return client.wait_for_deployment(
            case["id"], options=DeploymentWaitOptions(**common, stop_on_blocked=case.get("stop_on_blocked", True))
        )

    try:
        pending = asyncio.create_task(wait())
        outcome = case.get("outcome")
        if outcome == "caller_cancelled":
            await asyncio.wait_for(entered.wait(), 1)
            pending.cancel("caller stopped")
            with pytest.raises(asyncio.CancelledError, match="caller stopped"):
                await asyncio.wait_for(pending, 1)
        elif outcome in ("network", "callback"):
            with pytest.raises(type(network if outcome == "network" else callback_error)) as error:
                await pending
            assert error.value is (network if outcome == "network" else callback_error)
        elif outcome in ("not_found", "unauthorized"):
            with pytest.raises(ErrNotFound if outcome == "not_found" else ErrUnauthorized) as error:
                await pending
            assert error.value.tx_id == "original-tx"
        elif outcome:
            with pytest.raises(WaiterError) as error:
                await asyncio.wait_for(pending, 1)
            assert error.value.code == outcome
            assert error.value.resource == case["resource"]
            assert error.value.id == case["id"]
            assert error.value.polls == case["polls"]
            last = error.value.last_observed
            assert (
                (last.status if case["resource"] == "build" else last.phase) == case["last"]
                if last
                else case["last"] is None
            )
            if outcome == "timeout":
                assert isinstance(error.value, (TimeoutError, RequestTimeoutError, WaiterTimeoutError))
        else:
            result = await pending
            assert (result.status if case["resource"] == "build" else result.phase) == (
                "succeeded" if case["resource"] == "build" else "released"
            )
        if case.get("resume"):
            assert (await wait()).phase == "released"
        await asyncio.sleep(0)
        assert calls == case["calls"]
        assert progress == case["progress"]
        if "attempts" in case:
            assert [info.attempt for info in reports] == case["attempts"]
        if "closed" in case:
            assert body.closed == case["closed"]
        assert client.async_httpx_client.timeout == httpx.Timeout(0.001)
    finally:
        await close(client)


@pytest.mark.parametrize("key", ["timeout", "min_delay", "max_delay"])
@pytest.mark.parametrize("value", [None, 0, -1, True, float("nan"), float("inf"), 2**31, 10**400])
def test_invalid_timers(key, value):
    with pytest.raises((ValueError, TypeError)):
        BuildWaitOptions(**{**FAST, key: value})


@pytest.mark.parametrize("value", [0, -1, 0.1, True, float("nan"), float("inf"), 2**53])
def test_invalid_poll_limits(value):
    with pytest.raises(ValueError):
        BuildWaitOptions(timeout=1, max_polls=value)


def test_options_are_typed_immutable_and_require_explicit_budget():
    with pytest.raises(TypeError):
        BuildWaitOptions()
    with pytest.raises(TypeError):
        DeploymentWaitOptions(timeout=1, stop_on_blocked=1)
    with pytest.raises(ValueError):
        BuildWaitOptions(timeout=1, min_delay=2, max_delay=1)
    with pytest.raises(TypeError):
        BuildWaitOptions(timeout=1, on_progress=42)
    with pytest.raises(FrozenInstanceError):
        BuildWaitOptions(timeout=1).timeout = 2
    assert WaiterOptions[BuildResponse](timeout=1).timeout == 1


async def test_invalid_ids_options_and_preawait_cancellation_perform_no_http():
    calls = []
    client = client_for(lambda request: calls.append(request))
    try:
        for id in ("", None, 1):
            with pytest.raises(TypeError):
                client.wait_for_build(id, options=BuildWaitOptions(timeout=1))
        with pytest.raises(TypeError):
            client.wait_for_deployment(DEPLOYMENT_ID, options=BuildWaitOptions(timeout=1))
        with pytest.raises(TypeError):
            client.wait_for_build("id", options=None)
        pending = asyncio.create_task(client.wait_for_build("id", options=BuildWaitOptions(timeout=1)))
        pending.cancel("before starting")
        with pytest.raises(asyncio.CancelledError, match="before starting"):
            await pending
        assert not calls
    finally:
        await close(client)


async def test_progress_model_copies_and_error_snapshots_cannot_change_results():
    def mutate(value, info):
        value.status = "succeeded"
        value.additional_properties["private"] = "changed"

    client = client_for(lambda request: httpx.Response(200, json=build(status="failed")))
    try:
        with pytest.raises(WaiterError) as error:
            await client.wait_for_build("build/1", options=BuildWaitOptions(**FAST, on_progress=mutate))
        first = error.value.last_observed
        assert isinstance(first, BuildResponse) and first.status == "failed"
        assert "private" not in first.additional_properties
        first.status = "succeeded"
        assert error.value.last_observed.status == "failed"
        assert str(error.value) == "SDK build wait stopped: failed"
    finally:
        await close(client)
    snapshots = []

    def progress(value, info):
        snapshots.append(value)
        value.status = "failed"

    client = client_for(lambda request: httpx.Response(200, json=build()))
    try:
        result = await client.wait_for_build("build/1", options=BuildWaitOptions(**FAST, on_progress=progress))
        assert isinstance(result, BuildResponse) and result.status == "succeeded"
        assert result is not snapshots[0]
    finally:
        await close(client)


async def test_capture_configuration_and_reuse_pool_without_closing_it():
    seen, closed = [], []

    class Transport(httpx.AsyncBaseTransport):
        async def handle_async_request(self, request):
            seen.append((str(request.url), request.headers["Authorization"]))
            return httpx.Response(200, json=build("id", "running" if len(seen) == 1 else "succeeded"))

        async def aclose(self):
            closed.append(True)

    client = FaaSClient(base_url="https://before.test/prefix", token="before", httpx_args={"transport": Transport()})
    original = client.async_httpx_client
    operation = client.wait_for_build("id", options=BuildWaitOptions(**FAST))
    original.headers["Authorization"] = "Bearer after"
    original.base_url = "https://after.test/prefix"
    try:
        assert (await operation).status == "succeeded"
        assert not closed
        assert all(url.startswith("https://before.test/prefix/") and token == "Bearer before" for url, token in seen)
        await client.wait_for_build("id", options=BuildWaitOptions(**FAST))
        assert seen[-1][0].startswith("https://after.test/prefix/") and seen[-1][1] == "Bearer after"
        assert not closed and client.async_httpx_client is original
    finally:
        await close(client)
    assert len(closed) == 1


@pytest.mark.parametrize("action", ["timeout", "cancel"])
async def test_late_cancellation_resistant_progress_cannot_poll_again(action):
    started, release_callback, finished = asyncio.Event(), asyncio.Event(), asyncio.Event()
    calls = []

    async def progress(value, info):
        started.set()
        try:
            await release_callback.wait()
        except asyncio.CancelledError:
            await release_callback.wait()
        finished.set()

    client = client_for(lambda request: calls.append(request) or httpx.Response(200, json=build(status="running")))
    try:
        pending = asyncio.create_task(
            client.wait_for_build(
                "build/1",
                options=BuildWaitOptions(
                    timeout=0.02 if action == "timeout" else 2, min_delay=0.001, max_delay=0.001, on_progress=progress
                ),
            )
        )
        await started.wait()
        if action == "cancel":
            pending.cancel("stop callback")
        with pytest.raises(WaiterTimeoutError if action == "timeout" else asyncio.CancelledError):
            await asyncio.wait_for(pending, 1)
        release_callback.set()
        await asyncio.wait_for(finished.wait(), 1)
        await asyncio.sleep(0.01)
        assert len(calls) == 1
    finally:
        release_callback.set()
        await close(client)


async def test_callback_sdk_timeout_exception_is_preserved_and_callback_has_no_read_policy():
    reports, seen = [], []
    original = RequestTimeoutError("from callback")

    async def handler(request):
        seen.append(request)
        return httpx.Response(200, json=build("nested" if "nested" in request.url.path else "build/1"))

    client = client_for(handler)

    async def progress(value, info):
        # This request is outside the status read scope and does not inherit its observer.
        await client.async_httpx_client.get("/v1/builds/nested")
        raise original

    try:
        with pytest.raises(RequestTimeoutError) as error:
            await client.wait_for_build(
                "build/1", options=BuildWaitOptions(**FAST, on_progress=progress, on_response=reports.append)
            )
        assert error.value is original
        assert len(seen) == 2 and len(reports) == 1
    finally:
        await close(client)


async def test_poll_sleep_is_cancelled_and_default_timeouts_do_not_bound_waiter():
    first = asyncio.Event()
    calls = []

    async def handler(request):
        calls.append(request)
        await asyncio.sleep(0.01)  # Longer than constructor timeout; total waiter budget applies.
        first.set()
        return httpx.Response(200, json=build(status="queued"))

    client = client_for(handler, timeout=0.001)
    try:
        pending = asyncio.create_task(client.wait_for_build("build/1", options=BuildWaitOptions(timeout=2)))
        await first.wait()
        await asyncio.sleep(0.01)
        pending.cancel("stop delay")
        with pytest.raises(asyncio.CancelledError, match="stop delay"):
            await asyncio.wait_for(pending, 1)
        assert len(calls) == 1
    finally:
        await close(client)


async def test_concurrent_waiters_keep_policies_and_deadlines_local():
    counts, reports = {}, []
    release_long = asyncio.Event()

    async def handler(request):
        id = request.url.path.rsplit("/", 1)[-1]
        counts[id] = counts.get(id, 0) + 1
        if id in ("short", "long"):
            await release_long.wait()
        if id in ("retry", "single") and counts[id] == 1:
            return httpx.Response(503, json={"code": "capacity_unavailable"})
        return httpx.Response(200, json=build(id))

    client = client_for(handler, options=FaaSClientOptions(retry=RetryOptions(max_attempts=0)))
    try:
        short = asyncio.create_task(client.wait_for_build("short", options=BuildWaitOptions(timeout=0.02)))
        long = asyncio.create_task(client.wait_for_build("long", options=BuildWaitOptions(timeout=2)))
        retried = asyncio.create_task(
            client.wait_for_build(
                "retry", options=BuildWaitOptions(**FAST, retry=RetryPolicy(2, 0.001), on_response=reports.append)
            )
        )
        single = asyncio.create_task(client.wait_for_build("single", options=BuildWaitOptions(**FAST, retry=False)))
        with pytest.raises(WaiterTimeoutError):
            await short
        assert not long.done()
        release_long.set()
        results = await asyncio.gather(long, retried, single, return_exceptions=True)
        assert results[0].id == "long" and results[1].id == "retry"
        assert isinstance(results[2], Exception) and not isinstance(results[2], WaiterError)
        assert counts == {"short": 1, "long": 1, "retry": 2, "single": 1}
        assert [info.attempt for info in reports] == [1, 2]
    finally:
        release_long.set()
        await close(client)


async def test_poll_jitter_grows_with_a_cap(monkeypatch):
    import faas_sdk._waiters as waiters

    bounds, delays, calls = [], [], []
    original_sleep = asyncio.sleep

    def jitter(low, high):
        bounds.append((low, high))
        return high

    async def sleep(delay):
        delays.append(delay)
        await original_sleep(0)

    monkeypatch.setattr(waiters.random, "uniform", jitter)
    monkeypatch.setattr(waiters.asyncio, "sleep", sleep)

    def handler(request):
        calls.append(request)
        return httpx.Response(200, json=build(status="succeeded" if len(calls) == 6 else "running"))

    client = client_for(handler)
    try:
        assert (
            await client.wait_for_build(
                "build/1", options=BuildWaitOptions(timeout=2, min_delay=0.001, max_delay=0.005)
            )
        ).status == "succeeded"
        assert len(delays) == 5
        assert all(0.001 <= delay <= 0.005 for delay in delays)
        assert bounds[0] == (0.001, 0.001)
        assert bounds[-1] == (0.001, 0.005)
        assert [high for _, high in bounds] == sorted(high for _, high in bounds)
    finally:
        await close(client)


async def test_custom_transport_sdk_timeout_retains_identity():
    original = RequestTimeoutError("custom transport error")

    def handler(request):
        raise original

    client = client_for(handler)
    try:
        with pytest.raises(RequestTimeoutError) as error:
            await client.wait_for_build("build/1", options=BuildWaitOptions(**FAST))
        assert error.value is original
    finally:
        await close(client)


@pytest.mark.parametrize("action", ["timeout", "cancel"])
async def test_late_headers_are_retired_without_late_progress_or_response_reports(action):
    started, release_headers, returned = asyncio.Event(), asyncio.Event(), asyncio.Event()
    progress, reports = [], []

    class Stream(httpx.AsyncByteStream):
        closed = 0

        async def __aiter__(self):
            yield json.dumps(build()).encode()

        async def aclose(self):
            self.closed += 1

    body = Stream()

    async def handler(request):
        started.set()
        try:
            await release_headers.wait()
        except asyncio.CancelledError:
            await release_headers.wait()
        returned.set()
        return httpx.Response(200, stream=body)

    client = client_for(handler)
    try:
        operation = asyncio.create_task(
            client.wait_for_build(
                "build/1",
                options=BuildWaitOptions(
                    timeout=0.02 if action == "timeout" else 2,
                    on_progress=lambda value, info: progress.append(value),
                    on_response=reports.append,
                ),
            )
        )
        await started.wait()
        if action == "cancel":
            operation.cancel("stop late response")
        with pytest.raises(WaiterTimeoutError if action == "timeout" else asyncio.CancelledError):
            await asyncio.wait_for(operation, 1)
        release_headers.set()
        await asyncio.wait_for(returned.wait(), 1)
        await asyncio.sleep(0.01)
        assert body.closed == 1
        assert progress == reports == []
    finally:
        release_headers.set()
        await close(client)


@pytest.mark.parametrize("action", ["timeout", "cancel"])
async def test_native_http_accounts_are_isolated_and_stopped_reads_close_sockets(action):
    seen, connections = [], set()
    entered, disconnected = asyncio.Event(), asyncio.Event()

    async def serve(reader, writer):
        connections.add(writer)
        try:
            data = await reader.readuntil(b"\r\n\r\n")
            lines = data.decode().split("\r\n")
            url = httpx.URL("http://local" + lines[0].split()[1])
            headers = httpx.Headers(line.split(": ", 1) for line in lines[1:] if ": " in line)
            id = url.path.rsplit("/", 1)[-1]
            token = headers["Authorization"].removeprefix("Bearer ")
            seen.append((id, token))
            if id == "blocked":
                entered.set()
                assert await reader.read() == b""
                disconnected.set()
                return
            assert id.startswith(token + "-")
            attempts = sum(item == (id, token) for item in seen)
            body = json.dumps(build(id, "succeeded" if attempts == 2 else "running")).encode()
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
    alice = FaaSClient(base_url=base_url, token="alice", timeout=0.001)
    bob = FaaSClient(base_url=base_url, token="bob", timeout=0.001)
    try:
        stopped = asyncio.create_task(
            alice.wait_for_build("blocked", options=BuildWaitOptions(timeout=0.1 if action == "timeout" else 2))
        )
        await asyncio.wait_for(entered.wait(), 1)
        if action == "cancel":
            stopped.cancel("close real socket")
        with pytest.raises(WaiterTimeoutError if action == "timeout" else asyncio.CancelledError):
            await asyncio.wait_for(stopped, 1)
        await asyncio.wait_for(disconnected.wait(), 1)
        reports = [[] for _ in range(12)]
        operations = [
            (alice if i % 2 == 0 else bob).wait_for_build(
                ("alice-" if i % 2 == 0 else "bob-") + str(i),
                options=BuildWaitOptions(**FAST, max_polls=2, retry=False, on_response=reports[i].append),
            )
            for i in range(12)
        ]
        results = await asyncio.gather(*operations)
        assert all(value.status == "succeeded" for value in results)
        assert all(len(values) == 2 for values in reports)
        assert len(seen) == 25
    finally:
        await close(alice)
        await close(bob)
        server.close()
        await server.wait_closed()
        for writer in tuple(connections):
            writer.close()
        await asyncio.sleep(0)


@pytest.mark.parametrize("resource", ["build", "deployment"])
async def test_progress_and_error_copies_preserve_generated_serialization(resource):
    from faas_sdk.types import UNSET

    body = build(status="failed") if resource == "build" else release("held")
    if resource == "deployment":
        body["events"] = [dict(at="2026-11-14T13:20:00Z", kind="held", summary="held")]
    snapshots = []

    def progress(value, info):
        snapshots.append(value)
        serialized = value.to_dict()
        json.dumps(serialized)  # No copied Unset instances may leak into the wire dictionary.
        assert "revision" not in serialized and "cache_key_sha256" not in serialized
        if resource == "deployment":
            assert value.revision is UNSET
            assert value.events[0].actor is UNSET
            assert "actor" not in serialized["events"][0]

    client = client_for(lambda request: httpx.Response(200, json=body))
    try:
        with pytest.raises(WaiterError) as error:
            if resource == "build":
                await client.wait_for_build("build/1", options=BuildWaitOptions(**FAST, on_progress=progress))
            else:
                await client.wait_for_deployment(
                    DEPLOYMENT_ID, options=DeploymentWaitOptions(**FAST, on_progress=progress)
                )
        last = error.value.last_observed
        assert len(snapshots) == 1 and last is not snapshots[0]
        assert json.loads(json.dumps(last.to_dict())) == snapshots[0].to_dict()
    finally:
        await close(client)


async def test_oversized_wire_integer_is_a_typed_invalid_response():
    body = release()
    body["traffic_percent"] = 10**400
    client = client_for(lambda request: httpx.Response(200, json=body))
    try:
        with pytest.raises(WaiterError) as error:
            await client.wait_for_deployment(DEPLOYMENT_ID, options=DeploymentWaitOptions(**FAST))
        assert error.value.code == "invalid_response"
        assert error.value.last_observed is None
    finally:
        await close(client)
