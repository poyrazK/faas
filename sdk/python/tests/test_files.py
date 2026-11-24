"""Shared file transfer protocol and managed job-store integrity."""

import asyncio
import hashlib
import json
from pathlib import Path
from urllib.parse import unquote

import httpx
import pytest
from faas_sdk.preview import FaaSClient
from faas_sdk.preview.files import (
    BytesFileSource,
    FileBucket,
    FileCheckpoint,
    FileError,
    FileOptions,
    file_source_digest,
)

from faas_sdk import JobContext, JobError

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "files.json").read_text())
DATA = F["source"].encode()
OPTIONS = FileOptions(timeout=3, max_polls=2, poll_delay=0)


class Harness:
    def __init__(self, scenario="success", token="alice"):
        self.scenario, self.token, self.state = scenario, token, "active"
        self.puts = self.completes = self.reads = 0
        self.parts, self.objects = {}, {}
        self.receipt_key, self.receipt_size = F["key"], 0
        self.client = FaaSClient(
            base_url=F["base_url"], token=token, httpx_args={"transport": httpx.MockTransport(self.control)}
        )
        self.files = FileBucket(
            self.client, app_slug=F["app_slug"], bucket=F["bucket"], data_transport=httpx.MockTransport(self.data)
        )

    def session(self):
        return dict(
            id=F["upload_id"],
            key=F["key"],
            size_bytes=len(DATA),
            part_size_bytes=F["part_size"],
            part_count=(len(DATA) + F["part_size"] - 1) // F["part_size"] + (1 if self.scenario == "bad_layout" else 0),
            content_type="text/csv",
            state=self.state,
            expires_at="2000-01-01T00:00:00Z" if self.scenario == "expired" else "2099-01-01T00:00:00Z",
            **({"etag": '"complete"'} if self.state == "completed" else {}),
        )

    def signed(self, method, key, part=None):
        from urllib.parse import quote

        return dict(
            method=method,
            url=("http://remote.test" if self.scenario == "insecure_url" else "https://objects.test")
            + "/"
            + quote(key, safe="")
            + ("?part=" + str(part) if part else ""),
            headers={"Authorization": "secret"}
            if self.scenario == "untrusted_headers"
            else {"Content-Type": "text/csv"},
            expires_at="2000-01-01T00:00:00Z" if self.scenario == "expired_capability" else "2099-01-01T00:00:00Z",
            **({"upload_id": F["receipt_id"]} if method == "PUT" and not part else {}),
        )

    async def control(self, request):
        self.reads += 1
        assert request.headers["Authorization"] == "Bearer " + self.token
        p = request.url.path.removeprefix("/prefix")
        if self.scenario == "forbidden":
            return httpx.Response(403, json={"status": 403})
        if p == "/v1/apps/imports":
            return httpx.Response(200, json={"id": F["app_id"]})
        if p.endswith("/signed-url") and "/parts/" not in p:
            body = json.loads(request.content)
            self.receipt_key, self.receipt_size = body["key"], body.get("size_bytes", 0)
            if body.get("if_none_match") == "*" and body["key"] in self.objects:
                return httpx.Response(409, json={"status": 409})
            return httpx.Response(200, json=self.signed(body["method"], body["key"]))
        if "/write-receipts/" in p:
            return httpx.Response(
                200,
                json=dict(
                    id=F["receipt_id"],
                    bucket_id=F["app_id"] if self.scenario == "wrong_receipt" else F["bucket"],
                    key=self.receipt_key,
                    bytes=self.receipt_size,
                    operation="put",
                    status="pending" if self.scenario == "pending_receipt" else "completed",
                    etag='"put"',
                ),
            )
        if p.endswith("/objects"):
            if request.method == "DELETE":
                self.objects.pop(request.url.params.get("key"), None)
                return httpx.Response(204)
            if "cursor" in request.url.params:
                assert request.url.params["cursor"] == F["cursor"]
            return httpx.Response(
                200,
                json=dict(
                    items=[dict(key=F["key"], size_bytes=len(DATA), last_modified="2026-01-01T00:00:00Z")],
                    next_cursor=F["cursor"],
                ),
            )
        if p.endswith("/parts"):
            return httpx.Response(
                200,
                json={
                    "items": [
                        dict(part_number=n, etag=v[0], size_bytes=len(v[1])) for n, v in sorted(self.parts.items())
                    ]
                },
            )
        if "/parts/" in p and p.endswith("/signed-url"):
            return httpx.Response(200, json=self.signed("PUT", F["key"], int(p.split("/")[-2])))
        if p.endswith("/complete"):
            self.completes += 1
            assert json.loads(request.content)["parts"] == [
                dict(part_number=n, etag=v[0]) for n, v in sorted(self.parts.items())
            ]
            self.state = "completing" if self.scenario == "completing" else "completed"
            self.objects[F["key"]] = b"".join(v[1] for n, v in sorted(self.parts.items()))
            if self.scenario == "lost_complete_ack" and self.completes == 1:
                raise httpx.ReadError("secret URL")
            return httpx.Response(200, json=self.session())
        if request.method == "DELETE":
            self.state = "aborted"
            return httpx.Response(204)
        return httpx.Response(200, json=self.session())

    async def data(self, request):
        assert "authorization" not in request.headers
        assert "cookie" not in request.headers
        key = unquote(request.url.path[1:])
        if request.method == "PUT":
            self.puts += 1
            data = await request.aread()
            tag = '"' + hashlib.sha256(data).hexdigest() + '"'
            if "part" in request.url.params:
                self.parts[int(request.url.params["part"])] = (tag, data)
            else:
                self.objects[key] = data
            if self.scenario == "lost_part_ack" and self.puts == 2:
                raise httpx.ReadError("secret signed URL")
            return httpx.Response(200, headers={"ETag": tag, "Set-Cookie": "secret=never-forward"})
        data = self.objects.get(key, DATA)
        return httpx.Response(
            200,
            content=b"" if request.method == "HEAD" else data,
            headers={"Content-Length": str(len(data)), "ETag": '"object"', "Content-Type": "text/csv"},
        )


@pytest.mark.parametrize("case", F["cases"], ids=lambda c: c["scenario"])
async def test_shared_file_protocol(case):
    h = Harness(case["scenario"])
    try:
        if case["error"]:
            with pytest.raises(FileError) as e:
                await h.files.upload(F["key"], BytesFileSource(DATA), options=OPTIONS, content_type="text/csv")
            assert e.value.code == case["error"]
        else:
            result = await h.files.upload(F["key"], BytesFileSource(DATA), options=OPTIONS, content_type="text/csv")
            assert result.size_bytes == len(DATA)
            assert h.objects[F["key"]] == DATA
    finally:
        await h.client.aclose()


@pytest.mark.parametrize("scenario", ["lost_part_ack", "lost_complete_ack"])
async def test_resume_after_lost_ack(scenario):
    h, checkpoints = Harness(scenario), []
    try:
        with pytest.raises(FileError, match="acceptance_unknown"):
            await h.files.upload(
                F["key"],
                BytesFileSource(DATA),
                options=FileOptions(timeout=3, on_checkpoint=checkpoints.append),
                content_type="text/csv",
            )
        c = FileCheckpoint.from_dict(json.loads(json.dumps(checkpoints[-1].to_dict())))
        assert "objects.test" not in json.dumps(c.to_dict())
        await h.files.resume(c, BytesFileSource(DATA), options=OPTIONS)
        assert h.objects[F["key"]] == DATA
        assert h.completes == 1
        assert h.puts == (len(DATA) + F["part_size"] - 1) // F["part_size"] + (1 if scenario == "lost_part_ack" else 0)
    finally:
        await h.client.aclose()


async def test_changed_source_and_wrong_bucket():
    h, checkpoints = Harness("lost_part_ack"), []
    try:
        with pytest.raises(FileError):
            await h.files.upload(
                F["key"],
                BytesFileSource(DATA),
                options=FileOptions(timeout=3, on_checkpoint=checkpoints.append),
                content_type="text/csv",
            )
        c, before = checkpoints[-1], h.puts
        with pytest.raises(FileError, match="source_changed"):
            await h.files.resume(c, BytesFileSource(b"Z" + DATA[1:]), options=OPTIONS)
        with pytest.raises(FileError, match="invalid_checkpoint"):
            h.files.resume(
                FileCheckpoint.from_dict({**c.to_dict(), "bucket": F["app_id"]}), BytesFileSource(DATA), options=OPTIONS
            )
        assert h.puts == before
    finally:
        await h.client.aclose()


async def test_source_mutation_before_put():
    h = Harness()

    class MutatingSource:
        size, reads = len(DATA), 0

        async def read(self, start, end):
            self.reads += 1
            data = DATA[start:end]
            return b"Z" + data[1:] if self.reads > (len(DATA) + F["part_size"] - 1) // F["part_size"] else data

    try:
        with pytest.raises(FileError, match="source_changed"):
            await h.files.upload(F["key"], MutatingSource(), options=OPTIONS, content_type="text/csv")
        assert h.puts == 0
    finally:
        await h.client.aclose()


async def test_limits_deadlines_cancellation_and_callback():
    h = Harness()

    class HangingSource:
        size = len(DATA)

        async def read(self, start, end):
            await asyncio.Future()

    try:
        with pytest.raises(FileError, match="limit"):
            await h.files.upload(F["key"], BytesFileSource(DATA), options=FileOptions(timeout=3, max_bytes=1))
        assert h.reads == 0
        with pytest.raises(FileError, match="limit"):
            await h.files.upload(F["key"], BytesFileSource(DATA), options=FileOptions(timeout=3, max_parts=1))
        with pytest.raises(FileError, match="deadline"):
            await h.files.upload(F["key"], HangingSource(), options=FileOptions(timeout=0.02))
        task = asyncio.create_task(h.files.upload(F["key"], HangingSource(), options=OPTIONS))
        await asyncio.sleep(0.01)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task

        def broken(_):
            raise ValueError("secret")

        with pytest.raises(FileError, match="callback_failed"):
            await h.files.upload(F["key"], BytesFileSource(DATA), options=FileOptions(timeout=3, on_checkpoint=broken))
        assert h.puts == 0
    finally:
        await h.client.aclose()


async def test_put_receipt_metadata_download_cursor_delete():
    h, receipts = Harness(), []
    try:
        await h.files.put(F["key"], DATA, options=OPTIONS, on_receipt=receipts.append)
        assert receipts == [F["receipt_id"]]
        await h.files.put("alice/empty", b"", options=OPTIONS)
        chunks = []
        assert await h.files.download_to(F["key"], chunks.append, options=OPTIONS) == len(DATA)
        assert b"".join(chunks) == DATA
        with pytest.raises(FileError, match="limit"):
            await h.files.download(F["key"], options=FileOptions(timeout=3, max_bytes=1))
        assert (await h.files.metadata(F["key"], options=OPTIONS))["size_bytes"] == len(DATA)
        page = await h.files.list(options=OPTIONS)
        assert page["next_cursor"] == F["cursor"]
        await h.files.list(options=OPTIONS, cursor=page["next_cursor"])
        await h.files.delete("alice/a?b#c.csv", options=OPTIONS)
    finally:
        await h.client.aclose()


@pytest.mark.parametrize("scenario,error", [("wrong_receipt", "invalid_response"), ("pending_receipt", "poll_limit")])
async def test_receipt_identity_and_pending_limit(scenario, error):
    h = Harness(scenario)
    try:
        with pytest.raises(FileError, match=error):
            await h.files.put(F["key"], DATA, options=OPTIONS)
    finally:
        await h.client.aclose()


async def test_concurrent_clients_and_job_store_integrity():
    a, b = Harness(token="alice"), Harness(token="bob")
    try:
        await asyncio.gather(
            *(h.files.upload(F["key"], BytesFileSource(DATA), options=OPTIONS, content_type="text/csv") for h in (a, b))
        )
        store = await a.files.job_store(options=OPTIONS)
        context, digest = JobContext("row", 1, lambda: None), hashlib.sha256(DATA).hexdigest()
        uri = await store.put(DATA, digest, context)
        assert uri == f"obj://{F['app_id']}/{F['bucket']}/jobs/{digest}"
        assert await store.get(uri, context) == DATA
        await store.put(DATA, digest, context)
        with pytest.raises(JobError, match="invalid_reference"):
            await store.get(uri.replace(F["app_id"], F["bucket"]), context)
        a.objects["jobs/" + digest] = b"corrupt"
        with pytest.raises(JobError, match="integrity_failed"):
            await store.get(uri, context)
    finally:
        await a.client.aclose()
        await b.client.aclose()


async def test_portable_checkpoint_and_fingerprint():
    v = json.loads((ROOT / "files-portable.json").read_text())
    assert await file_source_digest(BytesFileSource(DATA)) == v["fixed_block_digest"]
    assert FileCheckpoint.from_dict(v["checkpoint"]).to_dict() == v["checkpoint"]
    assert not hasattr(__import__("faas_sdk"), "FileBucket")


async def test_deadlines_own_body_and_checkpoint_callbacks():
    h = Harness()
    closed = []

    class StalledBody(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b"1"
            await asyncio.Future()

        async def aclose(self):
            closed.append(True)

    async def data(request):
        return httpx.Response(200, stream=StalledBody())

    files = FileBucket(h.client, app_slug=F["app_slug"], bucket=F["bucket"], data_transport=httpx.MockTransport(data))

    async def checkpoint(_):
        await asyncio.Future()

    try:
        with pytest.raises(FileError, match="deadline"):
            await files.download(F["key"], options=FileOptions(timeout=0.03))
        await asyncio.sleep(0.01)
        assert closed
        with pytest.raises(FileError, match="deadline"):
            await h.files.upload(
                F["key"], BytesFileSource(DATA), options=FileOptions(timeout=0.03, on_checkpoint=checkpoint)
            )
        assert h.puts == 0
    finally:
        await h.client.aclose()


async def test_uncertain_put_receipt_and_explicit_abort():
    h, receipts, checkpoints = Harness(), [], []

    async def lost(request):
        await h.data(request)
        raise httpx.ReadError("lost response")

    files = FileBucket(h.client, app_slug=F["app_slug"], bucket=F["bucket"], data_transport=httpx.MockTransport(lost))

    def stop(c):
        checkpoints.append(c)
        raise ValueError("stop")

    try:
        with pytest.raises(FileError, match="acceptance_unknown"):
            await files.put(F["key"], DATA, options=OPTIONS, on_receipt=receipts.append)
        await files.confirm(receipts[0], F["key"], len(DATA), options=OPTIONS)
        assert h.puts == 1
        with pytest.raises(FileError, match="callback_failed"):
            await h.files.upload(F["key"], BytesFileSource(DATA), options=FileOptions(timeout=3, on_checkpoint=stop))
        await h.files.abort(checkpoints[0], options=OPTIONS)
        with pytest.raises(FileError, match="session_closed"):
            await h.files.resume(checkpoints[0], BytesFileSource(DATA), options=OPTIONS)
    finally:
        await h.client.aclose()
