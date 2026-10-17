"""Portable ownership/recovery tests; run from a standalone materialized starter."""

import asyncio
import importlib.abc
import json
import os
import signal
import stat
import sys
import tempfile
import unittest
from contextlib import AsyncExitStack
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class NoRepositorySDK(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname == "faas_sdk" or fullname.startswith("faas_sdk."):
            raise AssertionError("Starter depends on a separately installed SDK")


sys.meta_path.insert(0, NoRepositorySDK())

import httpx
from app import create_app
from runtime.async_secret_reload_runtime import AsyncSecretReloadIO
from storage import UPLOAD_BYTES, OwnedS3, Storage, StorageError

TOKEN = "first-private-application-token-12345678"
SECOND_TOKEN = "second-private-application-token-12345678"


class Body:
    def __init__(self, client, *, gate=None, data=b"binary\x00contents"):
        self.client, self.gate, self.data = client, gate, data
        self.closed = self.reads = 0

    async def read(self, _size):
        self.reads += 1
        if self.reads == 1:
            return self.data[:3] if self.gate else self.data
        if self.reads == 2 and self.gate:
            self.client.entered.set()
            await self.gate.wait()
            return self.data[3:]
        return b""

    def close(self):
        self.closed += 1


class Client:
    def __init__(self, owner, config):
        self.owner, self.config = owner, config
        self.closed = 0
        self.bodies, self.puts = [], []
        self.entered = asyncio.Event()
        self.get_gate = self.body_gate = self.put_gate = None
        self.ignore_cancel = False
        self.spool = None

    async def pause(self, gate):
        if gate is not None:
            self.entered.set()
            try:
                await gate.wait()
            except asyncio.CancelledError:
                if not self.ignore_cancel:
                    raise
                await gate.wait()

    async def list_objects_v2(self, **options):
        assert options == {"Bucket": "assets", "MaxKeys": 1}
        await self.pause(self.owner.validate_gate)
        if self.owner.invalid:
            raise RuntimeError("private-provider-credential-error")
        return {}

    async def get_object(self, **options):
        await self.pause(self.get_gate)
        body = Body(self, gate=self.body_gate)
        self.bodies.append(body)
        return {"Body": body, "ContentLength": len(body.data), "ContentType": "text/html"}

    async def put_object(self, **options):
        self.spool = spool = options["Body"]
        self.puts.append(options)
        assert stat.S_IMODE(os.fstat(spool.fileno()).st_mode) == 0o600
        parent = next(Path(self.owner.root).iterdir())
        assert stat.S_IMODE(parent.stat().st_mode) == 0o700
        await self.pause(self.put_gate)
        assert not spool.closed and not self.closed
        self.owner.uploads.append(spool.read())


class Harness:
    def __init__(self, root):
        self.root = root
        self.env = {"FAAS_SECRETS_SNAPSHOT_FILE": "/run/secrets.json", "FAAS_SECRETS_RELOAD_READY_FILE": "/run/ready",
                    "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": "http://127.0.0.1:4100/ack",
                    "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32}
        self.values = {"GREGALE_S3_ASSETS_ENDPOINT": "https://gateway.example", "GREGALE_S3_ASSETS_REGION": "eu-test-1",
                       "GREGALE_S3_ASSETS_BUCKET": "assets", "GREGALE_S3_ASSETS_ACCESS_KEY_ID": "access-first",
                       "GREGALE_S3_ASSETS_SECRET_ACCESS_KEY": "private-first-secret",
                       "GREGALE_S3_ASSETS_ADDRESSING_STYLE": "path", "STORAGE_APP_TOKEN": TOKEN}
        self.revision = "a" * 64
        self.clients, self.acks, self.uploads = [], [], []
        self.validate_gate = None
        self.invalid = self.ignore_cancel = False

    async def factory(self, config, cancel):
        cancel.check()
        client = Client(self, config)
        client.ignore_cancel = self.ignore_cancel
        self.clients.append(client)
        stack = AsyncExitStack()

        async def close():
            assert all(body.closed for body in client.bodies)
            assert client.spool is None or client.spool.closed
            client.closed += 1

        stack.push_async_callback(close)
        return OwnedS3(client, stack)

    async def read(self, _path, cancel):
        cancel.check()
        return json.dumps({"revision": self.revision, "secrets": self.values})

    async def ready(self, _path, cancel):
        cancel.check()

    async def post(self, _endpoint, body, cancel):
        cancel.check()
        self.acks.append(json.loads(body))
        return 202

    def service(self, **options):
        return Storage(env=self.env, io=AsyncSecretReloadIO(read=self.read, ready=self.ready, post=self.post),
                       client_factory=self.factory, spool_root=self.root, **options)


async def until(predicate):
    async with asyncio.timeout(2):
        while not predicate():
            await asyncio.sleep(0.001)


async def exchange(app, *, method="GET", path="/objects/file.bin", headers=(), client="198.51.100.2", body=b"", more_body=False):
    incoming, outgoing = asyncio.Queue(), asyncio.Queue()
    await incoming.put({"type": "http.request", "body": body, "more_body": more_body})
    scope = {"type": "http", "asgi": {"version": "3.0"}, "http_version": "1.1", "method": method,
             "scheme": "http", "path": path, "raw_path": path.encode(), "query_string": b"", "root_path": "",
             "headers": [(b"authorization", ("Bearer " + TOKEN).encode()), *headers],
             "client": (client, 1234), "server": ("app", 80)}
    task = asyncio.create_task(app(scope, incoming.get, outgoing.put))
    return task, incoming, outgoing


class StorageTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
        self.addCleanup(signal.signal, signal.SIGHUP, previous)
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.h = Harness(self.directory.name)

    async def application(self, **options):
        self.service = self.h.service(**options)
        app = create_app(lambda: self.service)
        context = app.router.lifespan_context(app)
        await context.__aenter__()
        self.addAsyncCleanup(context.__aexit__, None, None, None)
        return app

    async def http(self, app, *, peer="198.51.100.2"):
        client = httpx.AsyncClient(transport=httpx.ASGITransport(app, client=(peer, 1)), base_url="http://app",
                                   headers={"Authorization": "Bearer " + TOKEN})
        await client.__aenter__()
        self.addAsyncCleanup(client.__aexit__, None, None, None)
        return client

    async def test_private_routes_and_opaque_downloads(self):
        app = await self.application()
        client = await self.http(app)
        self.assertEqual((await client.get("/objects/file.bin", headers={"authorization": "wrong"})).status_code, 401)
        response = await client.get("/objects/file.bin")
        self.assertEqual(response.content, b"binary\x00contents")
        self.assertEqual(response.headers["content-type"], "application/octet-stream")
        self.assertEqual(response.headers["content-disposition"], "attachment")
        self.assertEqual(response.headers["x-content-type-options"], "nosniff")
        for path in ("/objects/sub/file", "/objects/file?list=1", "/objects/%00", "/objects/" + "a" * 129):
            self.assertEqual((await client.get(path)).status_code, 400)
        self.assertEqual((await client.get("/healthz")).status_code, 200)
        self.assertEqual((await client.get("/readyz")).status_code, 200)
        self.assertEqual((await client.delete("/objects/file")).status_code, 405)
        self.assertEqual(self.h.acks[0], {"status": "applied", "revision": "a" * 64, "generation": "a" * 32})

    async def test_upload_spool_exact_bytes_size_limits_and_cleanup(self):
        app = await self.application()
        client = await self.http(app)
        payload = bytes(range(256)) * 257
        self.assertEqual((await client.put("/objects/file.bin", content=payload)).status_code, 201)
        self.assertEqual(self.h.uploads, [payload])
        self.assertEqual(list(Path(self.directory.name).iterdir()), [])
        self.assertTrue(self.h.clients[0].spool.closed)
        self.assertEqual((await client.put("/objects/file", content=b"", headers={"content-length": str(UPLOAD_BYTES + 1)})).status_code, 413)

        async def oversized():
            for _ in range(11):
                yield b"x" * (1024 * 1024)

        self.assertEqual((await client.put("/objects/file", content=oversized())).status_code, 413)
        self.assertEqual((await client.put("/objects/file", content=b"short", headers={"content-length": "8"})).status_code, 400)
        self.assertEqual(len(self.h.clients[0].puts), 1)
        self.assertEqual(list(Path(self.directory.name).iterdir()), [])

    async def test_rotation_keeps_stream_leased_and_rotates_http_authentication(self):
        app = await self.application()
        old = self.h.clients[0]
        old.body_gate = asyncio.Event()
        task, _incoming, outgoing = await exchange(app)
        self.assertEqual((await outgoing.get())["status"], 200)
        initial = (await outgoing.get())["body"]
        self.h.values.update(STORAGE_APP_TOKEN=SECOND_TOKEN, GREGALE_S3_ASSETS_ACCESS_KEY_ID="access-second")
        self.h.revision = "b" * 64
        await self.service.coordinator.reload()
        self.assertEqual(old.closed, 0)
        client = await self.http(app)
        self.assertEqual((await client.get("/objects/file")).status_code, 401)
        self.assertEqual((await client.get("/objects/file", headers={"authorization": "Bearer " + SECOND_TOKEN})).status_code, 200)
        old.body_gate.set()
        await task
        tail = bytearray()
        while not outgoing.empty():
            tail.extend((await outgoing.get()).get("body", b""))
        self.assertEqual(initial + tail, b"binary\x00contents")
        await self.service.binding.wait_for_retirement(1)
        self.assertEqual(old.closed, 1)

    async def test_upload_capacity_and_rotation_retain_spools_until_driver_finishes(self):
        app = await self.application()
        old = self.h.clients[0]
        old.put_gate = asyncio.Event()
        first = await exchange(app, method="PUT", body=b"first")
        second = await exchange(app, method="PUT", body=b"second")
        await until(lambda: len(old.puts) == 2)
        client = await self.http(app)
        self.assertEqual((await client.put("/objects/third", content=b"third")).status_code, 503)
        self.assertEqual(len(list(Path(self.directory.name).iterdir())), 2)
        await self.service.coordinator.reload()
        self.assertEqual(old.closed, 0)
        old.put_gate.set()
        await asyncio.gather(first[0], second[0])
        self.assertCountEqual(self.h.uploads, [b"first", b"second"])
        await self.service.binding.wait_for_retirement(1)
        self.assertEqual(old.closed, 1)
        self.assertEqual(list(Path(self.directory.name).iterdir()), [])

    async def test_disconnect_closes_body_before_client_retirement(self):
        app = await self.application()
        old = self.h.clients[0]
        old.body_gate = asyncio.Event()
        task, incoming, outgoing = await exchange(app)
        await outgoing.get()
        await outgoing.get()
        await incoming.put({"type": "http.disconnect"})
        await task
        self.service.binding.invalidate()
        await self.service.binding.wait_for_retirement(1)
        self.assertEqual(old.bodies[0].closed, 1)
        self.assertEqual(old.closed, 1)

    async def test_upload_disconnect_removes_partial_spool_without_provider_put(self):
        app = await self.application()
        task, incoming, _outgoing = await exchange(app, method="PUT", body=b"partial", more_body=True)
        await until(lambda: bool(list(Path(self.directory.name).iterdir())))
        await incoming.put({"type": "http.disconnect"})
        await task
        self.assertEqual(self.h.clients[0].puts, [])
        self.assertEqual(list(Path(self.directory.name).iterdir()), [])

    async def test_total_transfer_capacity_is_bounded(self):
        app = await self.application()
        old = self.h.clients[0]
        old.body_gate = asyncio.Event()
        transfers = [await exchange(app) for _ in range(16)]
        for _task, _incoming, outgoing in transfers:
            self.assertEqual((await outgoing.get())["status"], 200)
            await outgoing.get()
        client = await self.http(app)
        self.assertEqual((await client.get("/objects/excess")).status_code, 503)
        old.body_gate.set()
        await asyncio.gather(*(task for task, _incoming, _outgoing in transfers))
        self.assertEqual(len(old.bodies), 16)

    async def test_native_cancellation_owns_late_get_without_sending_headers(self):
        app = await self.application()
        old = self.h.clients[0]
        old.get_gate, old.ignore_cancel = asyncio.Event(), True
        task, _incoming, outgoing = await exchange(app)
        await old.entered.wait()
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.service.binding.invalidate()
        self.assertEqual(old.closed, 0)
        old.get_gate.set()
        await self.service.binding.wait_for_retirement(1)
        self.assertTrue(outgoing.empty())
        self.assertEqual(old.bodies[0].closed, 1)
        self.assertEqual(old.closed, 1)

    async def test_transfer_deadline_retains_late_put_spool_without_replay(self):
        app = await self.application(transfer_timeout=0.04)
        old = self.h.clients[0]
        old.put_gate, old.ignore_cancel = asyncio.Event(), True
        task, _incoming, outgoing = await exchange(app, method="PUT", body=b"one-write")
        await old.entered.wait()
        await asyncio.wait_for(task, 0.5)
        self.assertEqual((await outgoing.get())["status"], 503)
        self.assertFalse(old.spool.closed)
        self.service.binding.invalidate()
        self.assertEqual(old.closed, 0)
        old.put_gate.set()
        await self.service.binding.wait_for_retirement(1)
        self.assertTrue(old.spool.closed)
        self.assertEqual(old.closed, 1)
        self.assertEqual(len(old.puts), 1)
        self.assertEqual(self.h.uploads, [b"one-write"])
        self.assertEqual(list(Path(self.directory.name).iterdir()), [])

    async def test_same_revision_restore_coalesces_and_fences_captured_stream(self):
        app = await self.application()
        old = self.h.clients[0]
        old.body_gate = asyncio.Event()
        stream, _incoming, outgoing = await exchange(app)
        await outgoing.get()
        await outgoing.get()
        self.h.validate_gate = asyncio.Event()
        first = asyncio.create_task(self.service.restore())
        await until(lambda: len(self.h.clients) == 2 and self.h.clients[-1].entered.is_set())
        self.assertFalse(self.service.ready())
        second = asyncio.create_task(self.service.restore())
        await asyncio.sleep(0)
        self.h.validate_gate.set()
        await asyncio.gather(first, second)
        await asyncio.gather(stream, return_exceptions=True)
        self.assertTrue(self.service.ready())
        self.assertEqual(self.service.generation, 1)
        self.assertEqual(len(self.h.clients), 2)
        self.assertEqual(old.closed, 1)
        self.assertEqual(old.bodies[0].closed, 1)
        self.assertEqual([ack["revision"] for ack in self.h.acks], ["a" * 64, "a" * 64])

    async def test_failed_restore_stays_closed_through_reload_until_successful_restore(self):
        await self.application()
        self.h.invalid = True
        with self.assertRaises(StorageError):
            await self.service.restore()
        self.assertFalse(self.service.ready())
        self.assertEqual(self.h.acks[-1]["status"], "failed")
        self.h.invalid = False
        await self.service.coordinator.reload()
        self.assertFalse(self.service.ready())
        await self.service.restore()
        self.assertTrue(self.service.ready())

    async def test_timed_out_restore_fences_late_validation_and_recovers(self):
        await self.application(restore_timeout=0.04)
        self.h.validate_gate, self.h.ignore_cancel = asyncio.Event(), True
        with self.assertRaises(StorageError):
            await self.service.restore()
        self.assertFalse(self.service.ready())
        self.assertEqual(len(self.h.acks), 1)
        late = self.h.clients[-1]
        self.assertEqual(late.closed, 0)
        self.h.validate_gate.set()
        await self.service.binding.wait_for_retirement(1)
        self.assertEqual(late.closed, 1)
        self.assertEqual(len(self.h.acks), 1)
        self.h.validate_gate = None
        await self.service.restore()
        self.assertTrue(self.service.ready())

    async def test_queued_restore_timeout_does_not_cancel_another_reload(self):
        await self.application(restore_timeout=0.04)
        self.h.validate_gate = asyncio.Event()
        other = asyncio.create_task(self.service.coordinator.reload())
        await until(lambda: len(self.h.clients) == 2 and self.h.clients[-1].entered.is_set())
        with self.assertRaises(StorageError):
            await self.service.restore()
        self.h.validate_gate.set()
        # The restore deliberately invalidated the earlier candidate. It cannot
        # reopen application readiness regardless of its eventual reload result.
        await asyncio.gather(other, return_exceptions=True)
        self.assertFalse(self.service.ready())
        self.h.validate_gate = None
        await self.service.restore()
        self.assertTrue(self.service.ready())

    async def test_restore_cancellation_and_peer_authentication(self):
        app = await self.application()
        client = await self.http(app)
        headers = {"X-Faas-After-Restore": "1", "X-Forwarded-For": "127.0.0.1"}
        self.assertEqual((await client.post("/internal/after-restore", headers=headers)).status_code, 403)
        local = await self.http(app, peer="127.0.0.1")
        self.assertEqual((await local.post("/internal/after-restore")).status_code, 403)
        self.assertEqual((await local.post("/internal/after-restore", headers=headers)).status_code, 204)
        before = len(self.h.acks)
        self.h.validate_gate = asyncio.Event()
        task, incoming, outgoing = await exchange(app, method="POST", path="/internal/after-restore", client="127.0.0.1",
                                                  headers=[(b"x-faas-after-restore", b"1")])
        await until(lambda: self.h.clients[-1].entered.is_set())
        await incoming.put({"type": "http.disconnect"})
        await task
        self.assertEqual((await outgoing.get())["status"], 503)
        self.assertFalse(self.service.ready())
        self.assertEqual(len(self.h.acks), before)
        self.h.validate_gate.set()
        await self.service.restore()
        self.assertTrue(self.service.ready())

    async def test_invalid_auth_and_revocation_invalidate_all_consumers(self):
        app = await self.application()
        client = await self.http(app)
        self.h.values["STORAGE_APP_TOKEN"] = "short"
        with self.assertRaises(Exception):
            await self.service.coordinator.reload()
        self.assertEqual((await client.get("/objects/file")).status_code, 503)
        with self.assertRaises(Exception):
            self.service.binding.acquire()
        self.assertEqual(self.h.acks[-1]["status"], "failed")
        self.h.values["STORAGE_APP_TOKEN"] = TOKEN
        await self.service.coordinator.reload()
        self.h.values.pop("GREGALE_S3_ASSETS_ACCESS_KEY_ID")
        with self.assertRaises(Exception):
            await self.service.coordinator.reload()
        self.assertFalse(self.service.ready())
        self.assertEqual((await client.get("/healthz")).status_code, 200)
        self.assertEqual((await client.get("/readyz")).status_code, 503)

    async def test_shutdown_drains_admitted_stream_before_retirement(self):
        app = await self.application()
        old = self.h.clients[0]
        old.body_gate = asyncio.Event()
        task, _incoming, outgoing = await exchange(app)
        await outgoing.get()
        await outgoing.get()
        closing = asyncio.create_task(self.service.close())
        await asyncio.sleep(0)
        self.assertFalse(closing.done())
        self.assertFalse(self.service.ready())
        self.assertEqual(old.closed, 0)
        old.body_gate.set()
        await task
        await closing
        self.assertEqual(old.closed, 1)
        self.assertFalse(self.service._active)

    async def test_shutdown_deadline_does_not_force_close_a_late_driver(self):
        service = self.h.service(shutdown_timeout=0.04)
        await service.start()
        app = create_app()
        app.state.storage = service
        old = self.h.clients[0]
        old.get_gate, old.ignore_cancel = asyncio.Event(), True
        task, _incoming, outgoing = await exchange(app)
        try:
            await old.entered.wait()
            service.stop()
            self.assertFalse(service.ready())
            with self.assertRaisesRegex(StorageError, "storage_cleanup_incomplete"):
                await service.close()
            self.assertEqual(old.closed, 0)
        finally:
            old.get_gate.set()
            await service.coordinator.wait_stopped()
            await service.binding.wait_for_retirement(1)
            await task
        self.assertEqual(old.closed, 1)
        self.assertEqual(old.bodies[0].closed, 1)
        self.assertEqual((await outgoing.get())["status"], 503)
