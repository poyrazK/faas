"""Source transfer and deployment acceptance fixtures shared with Node."""

from __future__ import annotations

import asyncio
import json
from dataclasses import FrozenInstanceError, replace
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import FaaSClient as PreviewClient

from faas_sdk import (
    FaaSClient,
    SourceUploadCheckpoint,
    SourceUploadError,
    SourceUploadOptions,
    SourceUploadTimeoutError,
    load_source_upload_checkpoint,
    save_source_upload_checkpoint,
)
from faas_sdk.models.upload_deploy_options import UploadDeployOptions

SDK = Path(__file__).resolve().parents[1]
CONTRACT = SDK / "request-contracts/source-uploads.json"
if not CONTRACT.exists():
    CONTRACT = SDK.parent / "request-contracts/source-uploads.json"
FIXTURE = json.loads(CONTRACT.read_text())


@pytest.mark.parametrize("factory", [FaaSClient, PreviewClient], ids=["public", "preview"])
@pytest.mark.parametrize("case", FIXTURE["cases"], ids=lambda case: case["name"])
async def test_shared_source_upload_contract(tmp_path, factory, case):
    scenario = case["scenario"]
    path = tmp_path / "source.tar.gz"
    path.write_text(FIXTURE["source_text"])
    checkpoint_path = tmp_path / "checkpoint.json"
    source = FIXTURE["source_text"].encode()
    received = bytearray()
    state = "open"
    calls, keys, offsets, snapshots = [], [], [], []
    opened = commits = deployments = 0
    entered = asyncio.Event()
    once = set()
    receipt = None
    network = OSError("response lost")
    current = None
    saved = ""

    def load():
        return (
            SourceUploadCheckpoint.from_dict(json.loads(saved))
            if scenario.endswith("timeout")
            else load_source_upload_checkpoint(checkpoint_path)
        )

    def session():
        return {
            "upload_id": "session-1",
            "app_slug": "api",
            "chunk_size": FIXTURE["chunk_size"],
            "total_size": len(source),
            "received_bytes": len(received) + (1 if scenario == "bad_offset" else 0),
            "status": state,
            "expires_at": "2026-11-16T00:00:00Z",
            "deployment_id": FIXTURE["deployment_id"] if state == "committed" else None,
        }

    async def handler(request):
        nonlocal opened, commits, deployments, state, receipt
        assert request.headers["Authorization"] == "Bearer token-before"
        url = request.url.path.removeprefix("/prefix")
        calls.append((request.method, url))
        if request.method == "POST" and url == "/v1/uploads":
            keys.append(request.headers["Idempotency-Key"])
            data = json.loads(request.content)
            assert data["deploy_options"] == FIXTURE["metadata"]
            assert data["app_slug"] == "api" and data["total_size"] == len(source)
            opened = 1
            if scenario == "lost_start" and scenario not in once:
                once.add(scenario)
                raise network
            return httpx.Response(
                201,
                json={
                    "upload_id": "session-1",
                    "chunk_size": FIXTURE["chunk_size"],
                    "total_size": len(source),
                    "expires_at": "2026-11-16T00:00:00Z",
                },
            )
        if request.method == "GET" and url == "/v1/uploads/session-1":
            return httpx.Response(200, json=session())
        if request.method == "PATCH":
            assert request.headers["Content-Type"] == "application/offset+octet-stream"
            offset = int(request.headers["Upload-Offset"])
            data = request.content
            offsets.append(offset)
            assert offset == len(received) and len(data) <= FIXTURE["chunk_size"]
            assert data == source[offset : offset + len(data)]
            if scenario in ("patch_timeout", "patch_cancel"):
                entered.set()
                await asyncio.Event().wait()
            if scenario == "expired":
                state = "expired"
                return httpx.Response(410, json={"code": "upload_session_expired"})
            if scenario in ("lost_before", "conflict", "recovery_limit") and scenario not in once:
                once.add(scenario)
                if scenario == "conflict":
                    return httpx.Response(409, json={"code": "upload_session_offset_conflict"})
                raise network
            if scenario == "partial" and scenario not in once:
                once.add(scenario)
                received.extend(data[:2])
                raise network
            received.extend(data)
            if scenario == "lost_chunk" and scenario not in once:
                once.add(scenario)
                raise network
            return httpx.Response(
                200, headers={"Upload-Offset": str(len(received)) if scenario != "bad_ack" else "4.0"}
            )
        if request.method == "GET" and "/deployment-submissions/" in url:
            assert current and url.endswith("/" + current.attempt_id)
            if receipt is None:
                return httpx.Response(404, json={"code": "not_found"})
            value = dict(receipt)
            if scenario == "bad_receipt":
                value["attempt_id"] = "wrong-attempt"
            return httpx.Response(200, json=value)
        if request.method == "POST" and url.endswith("/commit"):
            commits += 1
            assert current and request.headers["Deployment-Attempt-ID"] == current.attempt_id
            assert request.headers["Idempotency-Key"] == current.commit_key
            assert current.stage == "committing"
            assert load().stage == "committing"
            if scenario == "unknown_commit":
                raise network
            if scenario == "pending_commit":
                return httpx.Response(409, json={"code": "deployment_attempt_pending"})
            deployments += 1
            state = "committed"
            receipt = {
                "attempt_id": current.attempt_id,
                "app_id": FIXTURE["app_id"],
                "deployment_id": FIXTURE["deployment_id"],
                "accepted_at": "2026-11-14T14:45:00Z",
            }
            if scenario in ("commit_timeout", "commit_cancel"):
                entered.set()
                await asyncio.Event().wait()
            if scenario == "lost_commit":
                raise network
            return httpx.Response(201, json={"id": FIXTURE["deployment_id"]})
        pytest.fail(f"Unexpected request {request.method} {url}")

    def save(value):
        nonlocal current, saved
        current = value
        assert value.options_sha256 == FIXTURE["metadata_sha256"]
        assert "token-before" not in json.dumps(value.to_dict())
        with pytest.raises(FrozenInstanceError):
            value.stage = "new"
        if scenario == "save_fail":
            raise OSError("checkpoint storage unavailable")
        saved = json.dumps(value.to_dict())
        if not scenario.endswith("timeout"):
            save_source_upload_checkpoint(checkpoint_path, value)
        snapshots.append(value)
        if scenario == "changed_live" and value.stage == "new":
            path.write_bytes(b"x" * len(source))
        if (
            scenario in ("restart", "changed_restart", "wrong_base", "wrong_metadata")
            and value.received_bytes == 4
            and scenario not in once
        ):
            once.add(scenario)
            raise OSError("process stopped after durable checkpoint")

    client = factory(
        base_url="https://uploads.test/prefix",
        token="token-before",
        timeout=0.001,
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    options = SourceUploadOptions(
        timeout=0.5 if scenario == "patch_timeout" else 3,
        on_checkpoint=save,
        max_recoveries=0 if scenario == "recovery_limit" else 3,
    )
    metadata = UploadDeployOptions.from_dict(FIXTURE["metadata"])
    try:

        async def upload():
            nonlocal current
            return await client.upload_source(path, "api", options=options, deploy_options=metadata)

        if scenario in ("restart", "changed_restart", "wrong_base", "wrong_metadata"):
            with pytest.raises(SourceUploadError) as error:
                await upload()
            assert error.value.code == "checkpoint_failed"
            current = load()
            if scenario == "changed_restart":
                path.write_bytes(b"x" * len(source))
            if scenario == "wrong_base":
                client.async_httpx_client.base_url = "https://other.test/prefix"
            if scenario == "wrong_metadata":
                metadata.runtime = "node"
            before = len(calls)
            if case["outcome"]:
                with pytest.raises(SourceUploadError) as error:
                    await client.upload_source(
                        path, "api", options=options, checkpoint=current, deploy_options=metadata
                    )
                assert error.value.code == case["outcome"] and len(calls) == before
                return
            current = await client.upload_source(
                path, "api", options=options, checkpoint=current, deploy_options=metadata
            )
        elif scenario in ("patch_timeout", "patch_cancel"):
            pending = asyncio.create_task(upload())
            await asyncio.wait_for(entered.wait(), 1)
            if scenario == "patch_cancel":
                pending.cancel("caller stopped")
            with pytest.raises(SourceUploadTimeoutError if scenario == "patch_timeout" else asyncio.CancelledError):
                await asyncio.wait_for(pending, 1)
            assert commits == 0 and current.pending_end == 4
            return
        elif case["outcome"] and scenario not in (
            "unknown_commit",
            "pending_commit",
            "commit_timeout",
            "commit_cancel",
            "bad_receipt",
        ):
            with pytest.raises(SourceUploadError) as error:
                await upload()
            assert error.value.code == case["outcome"] and commits == 0
            if scenario == "save_fail":
                assert not calls
            return
        else:
            current = await upload()
        assert bytes(received) == source and current.stage == "ready" and commits == 0
        if scenario in ("commit_timeout", "commit_cancel"):
            pending = asyncio.create_task(
                client.commit_source(
                    current, options=replace(options, timeout=0.5 if scenario == "commit_timeout" else 3)
                )
            )
            await asyncio.wait_for(entered.wait(), 1)
            if scenario == "commit_cancel":
                pending.cancel("caller stopped")
            with pytest.raises(SourceUploadTimeoutError if scenario == "commit_timeout" else asyncio.CancelledError):
                await asyncio.wait_for(pending, 1)
            current = load()
            result = await client.commit_source(current, options=SourceUploadOptions(timeout=2, on_checkpoint=save))
        elif case["outcome"]:
            with pytest.raises(SourceUploadError) as error:
                await client.commit_source(current, options=options)
            assert error.value.code == case["outcome"]
            current = load()
            before = commits
            with pytest.raises(SourceUploadError):
                await client.commit_source(current, options=options)
            assert commits == before == 1
            return
        else:
            result = await client.commit_source(current, options=options)
        assert str(result.deployment_id) == FIXTURE["deployment_id"]
        assert commits == deployments == 1 and current.stage == "accepted"
        path.unlink()  # Receipt recovery requires no source file or fresh upload.
        await client.commit_source(load(), options=options)
        assert commits == 1 and opened == 1
        assert len(set(keys)) == 1
        if scenario == "lost_start":
            assert len(keys) == 2
        if scenario == "lost_chunk":
            assert offsets == [0, 4, 8]
        if scenario == "partial":
            assert offsets == [0, 2, 6]
    finally:
        await client.aclose()
        client.close()


def checkpoint():
    import hashlib

    from faas_sdk._source_upload import _options_hash

    return SourceUploadCheckpoint(
        1,
        "https://uploads.test/prefix",
        "api",
        9,
        hashlib.sha256(b"abcdefghi").hexdigest(),
        _options_hash({"present": False, "value": {}}),
        "start-key",
        "attempt-key",
        "commit-key",
    )


@pytest.mark.parametrize(
    "change",
    [
        {"version": True},
        {"total_size": True},
        {"received_bytes": 10},
        {"sha256_hex": "wrong"},
        {"base_url": "https://user:secret@test"},
        {"stage": "ready"},
        {"pending_end": 1},
        {"attempt_id": "bad\nheader"},
    ],
)
def test_invalid_checkpoints_are_rejected(change):
    with pytest.raises(ValueError):
        SourceUploadCheckpoint.from_dict({**checkpoint().to_dict(), **change})
    value = checkpoint().to_dict()
    del value["pending_end"]
    with pytest.raises(ValueError):
        SourceUploadCheckpoint.from_dict(value)


def test_checkpoint_file_roundtrip_is_private_and_atomic(tmp_path):
    import os

    path = tmp_path / "state.json"
    save_source_upload_checkpoint(path, checkpoint())
    assert load_source_upload_checkpoint(path) == checkpoint()
    if os.name != "nt":
        assert path.stat().st_mode & 0o777 == 0o600
    assert list(tmp_path.iterdir()) == [path]


@pytest.mark.parametrize("value", [0, -1, True, float("inf"), float("nan"), 10**400])
def test_invalid_upload_budgets(value):
    with pytest.raises(ValueError):
        SourceUploadOptions(timeout=value, on_checkpoint=lambda value: None)


async def test_synchronous_checkpoint_time_counts_before_network(tmp_path):
    import time

    path = tmp_path / "source"
    path.write_bytes(b"archive")
    calls = []

    def save(value):
        time.sleep(0.03)

    client = FaaSClient(
        base_url="https://uploads.test",
        httpx_args={"transport": httpx.MockTransport(lambda request: calls.append(request))},
    )
    try:
        with pytest.raises(SourceUploadTimeoutError):
            await client.upload_source(path, "api", options=SourceUploadOptions(timeout=0.01, on_checkpoint=save))
        assert not calls
    finally:
        await client.aclose()
        client.close()


@pytest.mark.parametrize("action", ["cancel", "timeout"])
async def test_late_checkpoint_callbacks_cannot_send_requests(tmp_path, action):
    entered, release, finished = asyncio.Event(), asyncio.Event(), asyncio.Event()
    calls = []
    path = tmp_path / "source"
    path.write_bytes(b"archive")

    async def save(value):
        entered.set()
        try:
            await release.wait()
        except asyncio.CancelledError:
            await release.wait()
        finished.set()

    client = FaaSClient(
        base_url="https://uploads.test",
        httpx_args={"transport": httpx.MockTransport(lambda request: calls.append(request))},
    )
    try:
        task = asyncio.create_task(
            client.upload_source(
                path, "api", options=SourceUploadOptions(timeout=0.1 if action == "timeout" else 2, on_checkpoint=save)
            )
        )
        await asyncio.wait_for(entered.wait(), 1)
        if action == "cancel":
            task.cancel("stop checkpoint")
        with pytest.raises(asyncio.CancelledError if action == "cancel" else SourceUploadTimeoutError):
            await asyncio.wait_for(task, 1)
        release.set()
        await asyncio.wait_for(finished.wait(), 1)
        await asyncio.sleep(0.01)
        assert not calls
    finally:
        release.set()
        await client.aclose()
        client.close()


async def test_native_http_concurrent_accounts_and_checkpoint_ownership(tmp_path):
    sessions, calls, writers = {}, [], set()

    async def serve(reader, writer):
        writers.add(writer)
        try:
            header = (await reader.readuntil(b"\r\n\r\n")).decode().split("\r\n")
            method, path, _ = header[0].split(" ")
            headers = httpx.Headers(line.split(": ", 1) for line in header[1:] if ": " in line)
            body = await reader.readexactly(int(headers.get("content-length", "0")))
            token = headers["Authorization"].removeprefix("Bearer ")
            calls.append((token, method, path))
            status, response_headers, result = 200, {}, {}
            if method == "POST" and path == "/v1/uploads":
                value = json.loads(body)
                id = token + "-" + str(len(sessions))
                sessions[id] = dict(token=token, received=bytearray(), total=value["total_size"])
                status, result = (
                    201,
                    dict(upload_id=id, chunk_size=4, total_size=value["total_size"], expires_at="2026-11-16T00:00:00Z"),
                )
            elif path.startswith("/v1/uploads/"):
                id = path.rsplit("/", 1)[-1]
                session = sessions[id]
                if session["token"] != token:
                    status, result = 403, {"code": "forbidden", "status": 403}
                elif method == "GET":
                    result = dict(
                        upload_id=id,
                        app_slug="api",
                        chunk_size=4,
                        total_size=session["total"],
                        received_bytes=len(session["received"]),
                        status="open",
                        expires_at="2026-11-16T00:00:00Z",
                    )
                else:
                    assert method == "PATCH" and int(headers["Upload-Offset"]) == len(session["received"])
                    session["received"].extend(body)
                    response_headers["Upload-Offset"] = str(len(session["received"]))
            else:
                status, result = 403, {"code": "forbidden", "status": 403}
            data = json.dumps(result).encode()
            output = f"HTTP/1.1 {status} Response\r\nContent-Type: application/json\r\nContent-Length: {len(data)}\r\nConnection: close\r\n"
            output += "".join(key + ": " + value + "\r\n" for key, value in response_headers.items()) + "\r\n"
            writer.write(output.encode() + data)
            await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()
            writers.discard(writer)

    server = await asyncio.start_server(serve, "127.0.0.1", 0)
    base = f"http://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    alice, bob = FaaSClient(base_url=base, token="alice"), FaaSClient(base_url=base, token="bob")
    path = tmp_path / "source"
    path.write_bytes(b"abcdefghi")
    try:
        values = await asyncio.gather(
            *[
                (alice if i % 2 == 0 else bob).upload_source(
                    path, "api", options=SourceUploadOptions(timeout=3, on_checkpoint=lambda value: None)
                )
                for i in range(6)
            ]
        )
        assert len(sessions) == 6 and all(bytes(session["received"]) == b"abcdefghi" for session in sessions.values())
        before = len(calls)
        with pytest.raises(SourceUploadError) as error:
            await bob.upload_source(
                path,
                "api",
                checkpoint=values[0],
                options=SourceUploadOptions(timeout=2, on_checkpoint=lambda value: None),
            )
        assert error.value.code == "request_failed" and error.value.cause.status == 403
        assert calls[before:] == [("bob", "GET", "/v1/uploads/" + values[0].upload_id)]
    finally:
        await alice.aclose()
        alice.close()
        await bob.aclose()
        bob.close()
        server.close()
        await server.wait_closed()
        for writer in tuple(writers):
            writer.close()
        await asyncio.sleep(0)


@pytest.mark.parametrize("preview", [False, True])
@pytest.mark.parametrize("kind", ["bytes", "file"])
async def test_generated_binary_append_preserves_wire_bytes_and_header(preview, kind):
    from io import BytesIO

    from faas_sdk.preview.api.uploads import append_upload as preview_append

    from faas_sdk.api.uploads import append_upload
    from faas_sdk.types import File

    module = preview_append if preview else append_upload
    raw = b"\x00\xff\x80archive"

    def handler(request):
        assert request.headers["Content-Type"] == "application/offset+octet-stream"
        assert request.headers["Upload-Offset"] == "7"
        assert request.content == raw
        return httpx.Response(200, headers={"Upload-Offset": str(7 + len(raw))})

    client = FaaSClient(base_url="https://binary.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        body = raw if kind == "bytes" else File(payload=BytesIO(raw))
        result = await module.asyncio_detailed("session", client=client.inner, body=body, upload_offset=7)
        assert result.parsed is None and result.headers["Upload-Offset"] == str(7 + len(raw))
        body = raw if kind == "bytes" else File(payload=BytesIO(raw))
        assert module.sync_detailed("session", client=client.inner, body=body, upload_offset=7).parsed is None
    finally:
        await client.aclose()
        client.close()


def test_binary_generator_adaptation_is_idempotent_and_fail_closed(tmp_path):
    import importlib.util

    script = SDK / "scripts/gen.py"
    spec = importlib.util.spec_from_file_location("sdk_gen_source", script)
    generator = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(generator)
    for prefix in ("", "preview/"):
        source = SDK / "faas_sdk" / prefix / "api/uploads/append_upload.py"
        target = tmp_path / prefix / "api/uploads/append_upload.py"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(source.read_bytes())
    before = {path: path.read_bytes() for path in tmp_path.rglob("*.py")}
    generator._adapt_source_upload_binary(tmp_path)
    assert before == {path: path.read_bytes() for path in before}
    path = next(iter(before))
    path.write_text(path.read_text().replace("else body.payload.read()", 'else b"wrong"'))
    with pytest.raises(ValueError, match="needs review"):
        generator._adapt_source_upload_binary(tmp_path)


async def test_resume_binds_metadata_presence(tmp_path):
    path = tmp_path / "source"
    path.write_bytes(b"archive")
    snapshots, calls = [], []

    def save(value):
        snapshots.append(value)
        raise OSError("interrupted")

    client = FaaSClient(
        base_url="https://metadata.test",
        httpx_args={"transport": httpx.MockTransport(lambda request: calls.append(request))},
    )
    try:
        options = SourceUploadOptions(timeout=1, on_checkpoint=save)
        with pytest.raises(SourceUploadError):
            await client.upload_source(path, "api", options=options)
        with pytest.raises(SourceUploadError) as error:
            await client.upload_source(
                path, "api", options=options, checkpoint=snapshots[-1], deploy_options=UploadDeployOptions()
            )
        assert error.value.code == "invalid_checkpoint" and not calls
    finally:
        await client.aclose()
        client.close()
