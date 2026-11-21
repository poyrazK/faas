from __future__ import annotations

import httpx
import pytest

from faas_sdk.preview import FaaSClient, awatch_execution

_EXECUTION_ID = "01234567-89ab-cdef-0123-456789abcdef"
_LIMITS = {
    "timeout_ms": 1000,
    "memory_mb": 128,
    "cpu_millicores": 250,
    "ephemeral_disk_mb": 64,
    "max_output_bytes": 1024,
    "pids_max": 32,
}


def _receipt(status: str, **extra: object) -> dict[str, object]:
    return {
        "id": _EXECUTION_ID,
        "status": status,
        "runtime": "node22",
        "limits": _LIMITS,
        "output_truncated": False,
        "created_at": "2026-01-01T00:00:00+00:00",
        **extra,
    }


def _stream(body: str) -> httpx.Response:
    return httpx.Response(
        200,
        headers={"content-type": "text/event-stream"},
        content=body.encode(),
    )


def test_watch_execution_decodes_and_resumes_with_latest_cursor() -> None:
    requests: list[httpx.Request] = []
    calls = 0

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal calls
        requests.append(request)
        calls += 1
        if calls == 1:
            return _stream(
                'id: 1\nevent: status\ndata: {"status":"running"}\n\n'
                'id: 2\nevent: stdout\ndata: {"chunk":"hello\\n"}\n\n'
            )
        return _stream('id: 3\nevent: terminal\ndata: {"status":"succeeded","exit_code":0}\n\n')

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    try:
        events = list(client.watch_execution("run/1", retry_initial=0, retry_max=0))
        assert [event.type for event in events] == ["status", "stdout", "terminal"]
        assert events[0].id == 1
        assert events[0].status == "running"
        assert events[1].chunk == "hello\n"
        assert events[2].data["exit_code"] == 0
        assert str(requests[0].url).endswith("/v1/executions/run%2F1/events?limit=100")
        assert requests[1].url.params["after"] == "2"
    finally:
        client.close()


@pytest.mark.asyncio
async def test_awatch_execution_uses_async_transport() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        return _stream('id: 7\nevent: terminal\ndata: {"status":"succeeded"}\n\n')

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    try:
        events = [event async for event in awatch_execution(client, "run-async", retry_initial=0, retry_max=0)]
        assert len(events) == 1
        assert events[0].type == "terminal"
        assert requests[0].headers["accept"] == "text/event-stream"
    finally:
        await client.async_httpx_client.aclose()


def test_run_execution_creates_streams_and_fetches_receipt() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.method == "POST" and request.url.path == "/v1/executions":
            assert request.headers["idempotency-key"] == "run-key-1"
            body = request.read().decode()
            assert "def main(input, context): return input" in body
            return httpx.Response(202, json=_receipt("queued"))
        if request.url.path == f"/v1/executions/{_EXECUTION_ID}/events":
            return _stream(
                'id: 1\nevent: status\ndata: {"status":"running"}\n\n'
                'id: 2\nevent: stdout\ndata: {"chunk":"hello"}\n\n'
                'id: 3\nevent: terminal\ndata: {"status":"succeeded"}\n\n'
            )
        if request.method == "GET" and request.url.path == f"/v1/executions/{_EXECUTION_ID}":
            return httpx.Response(
                200,
                json=_receipt(
                    "succeeded",
                    runtime="python313",
                    profile="python-data-v1",
                    runtime_image_digest="sha256:" + "a" * 64,
                    packages={"numpy": "2.5.3"},
                    result={"ok": True},
                    stdout="hello",
                ),
            )
        return httpx.Response(404)

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    try:
        seen: list[str] = []
        receipt = client.run_execution(
            {"runtime": "python313", "profile": "python-data-v1", "source": "def main(input, context): return input"},
            on_event=lambda event: seen.append(event.type),
            idempotency_key="run-key-1",
            retry_initial=0,
            retry_max=0,
        )
        assert str(receipt.id) == _EXECUTION_ID
        assert receipt.status == "succeeded"
        assert receipt.profile == "python-data-v1"
        assert receipt.packages.to_dict() == {"numpy": "2.5.3"}
        assert receipt.runtime_image_digest == "sha256:" + "a" * 64
        assert '"profile":"python-data-v1"' in requests[0].content.decode().replace(" ", "")
        assert receipt.result == {"ok": True}
        assert seen == ["status", "stdout", "terminal"]
        assert [request.method for request in requests] == ["POST", "GET", "GET"]
    finally:
        client.close()


@pytest.mark.asyncio
async def test_arun_execution_awaits_callback_and_returns_receipt() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.method == "POST":
            return httpx.Response(202, json=_receipt("queued"))
        if request.url.path.endswith("/events"):
            return _stream('id: 1\nevent: terminal\ndata: {"status":"succeeded"}\n\n')
        return httpx.Response(200, json=_receipt("succeeded", result=42))

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    seen: list[str] = []

    async def on_event(event: object) -> None:
        seen.append(getattr(event, "type"))

    try:
        receipt = await client.arun_execution(
            {"runtime": "node22", "source": "42"},
            on_event=on_event,
            retry_initial=0,
            retry_max=0,
        )
        assert receipt.status == "succeeded"
        assert receipt.result == 42
        assert seen == ["terminal"]
        assert len(requests) == 3
    finally:
        await client.aclose()


def test_decode_execution_artifact_validates_binary_content() -> None:
    import base64
    import hashlib

    from faas_sdk.preview import decode_execution_artifact
    from faas_sdk.preview.models.execution_artifact import ExecutionArtifact

    content = bytes([0, 1, 255])
    artifact = ExecutionArtifact(
        name="result.bin",
        size_bytes=3,
        sha256="sha256:" + hashlib.sha256(content).hexdigest(),
        content=base64.b64encode(content).decode(),
    )
    assert decode_execution_artifact(artifact) == content
    artifact.size_bytes = 4
    with pytest.raises(ValueError):
        decode_execution_artifact(artifact)
    artifact.size_bytes = 3
    artifact.sha256 = "sha256:bad"
    with pytest.raises(ValueError):
        decode_execution_artifact(artifact)


def test_extended_run_uses_v2_and_preserves_invalid_contract_receipt() -> None:
    paths: list[str] = []
    def handler(request: httpx.Request) -> httpx.Response:
        paths.append(request.url.path)
        if request.method == "POST":
            assert request.url.path == "/v2/executions"
            return httpx.Response(202, json=_receipt("queued"))
        if request.url.path.endswith("/events"):
            return _stream('id: 1\nevent: terminal\ndata: {"status":"succeeded"}\n\n')
        return httpx.Response(200, json=_receipt("succeeded", result="debug-value", result_validation={"status": "invalid", "message": "Run result does not match result_schema"}))
    client = FaaSClient(base_url="https://api.example.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        receipt = client.run_execution({"runtime": "node24", "source": "return 42", "result_schema": False}, idempotency_key="contract-key")
        assert str(receipt.status) == "succeeded"
        assert receipt.result == "debug-value"
        assert receipt.result_validation.status == "invalid"
        assert "/v1/executions" not in paths
    finally:
        client.close()


def test_file_helpers_stream_and_verify_immutable_content() -> None:
    import hashlib
    import io
    payload = b"run-file-bytes" * 150000
    digest = "sha256:" + hashlib.sha256(payload).hexdigest()
    file_id = "01234567-89ab-4def-8123-456789abcdef"
    meta = {"id": file_id, "status": "ready", "size_bytes": len(payload), "sha256": digest, "content_url": f"/v1/execution-files/{file_id}/content"}
    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "POST":
            return httpx.Response(200, json={**meta, "status": "missing", "upload_required": True})
        if request.method == "PUT":
            assert request.headers["x-content-sha256"] == digest
            assert int(request.headers["content-length"]) == len(payload)
            assert request.read() == payload
            return httpx.Response(200, json=meta)
        if request.url.path.endswith("/content"):
            return httpx.Response(200, content=payload)
        return httpx.Response(200, json=meta)
    client = FaaSClient(base_url="https://api.example.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        source = io.BytesIO(payload)
        prepared = client.prepare_execution_file(source)
        assert prepared.sha256 == digest and source.tell() == 0
        client.upload_execution_file(prepared, source)
        destination = io.BytesIO()
        client.download_execution_file(file_id, destination)
        assert destination.getvalue() == payload
    finally:
        client.close()


def test_five_minute_class_and_environment_use_v2() -> None:
    import json
    paths: list[str] = []
    def handler(request: httpx.Request) -> httpx.Response:
        paths.append(request.url.path)
        if request.url.path.endswith("/events"):
            return _stream('id: 1\nevent: terminal\ndata: {"status":"succeeded"}\n\n')
        if request.method != "POST":
            return httpx.Response(200, json=_receipt("succeeded", result=42))
        body = json.loads(request.content)
        assert body["environment_id"] == _EXECUTION_ID
        assert body["limits"] == {"execution_class": "extended", "timeout_ms": 300000}
        return httpx.Response(202, json=_receipt("succeeded", result=42))
    client = FaaSClient(base_url="https://api.example.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        result = client.run_execution({"runtime": "node24", "source": "return 42", "environment_id": _EXECUTION_ID, "limits": {"execution_class": "extended", "timeout_ms": 300000}})
        assert result.result == 42
        assert paths == ["/v2/executions", f"/v1/executions/{_EXECUTION_ID}/events", f"/v1/executions/{_EXECUTION_ID}"]
    finally:
        client.close()
