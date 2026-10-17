"""Actual aiobotocore and handler-process acceptance against owned signed S3."""

import asyncio
import hashlib
import json
import os
import signal
import socket
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import httpx
from app import create_app
from s3_fixture import SECOND_TOKEN, TOKEN, SignedS3, projection
from storage import Storage, StorageError
from test_storage import exchange, until


class SignedStorageTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
        self.addCleanup(signal.signal, signal.SIGHUP, previous)
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.spool = self.root / "spool"
        self.spool.mkdir()
        fixture, settings = await self.fixture()
        self.server = await self.enterAsyncContext(fixture)
        self.env, self.publish = projection(self.root, self.server)
        self.env.update(settings)

    async def fixture(self):
        return SignedS3(), {}

    async def application(self, **options):
        service = Storage(env=self.env, spool_root=str(self.spool), **options)
        app = create_app(lambda: service)
        await self.enterAsyncContext(app.router.lifespan_context(app))
        client = await self.enterAsyncContext(httpx.AsyncClient(transport=httpx.ASGITransport(app), base_url="http://app",
                                                               headers={"Authorization": "Bearer " + TOKEN}))
        return app, service, client

    async def test_real_sigv4_exact_upload_download_rotation_and_stale_credentials(self):
        with patch.dict(os.environ, {"AWS_ACCESS_KEY_ID": "ambient-must-not-be-used",
                                    "AWS_SECRET_ACCESS_KEY": "ambient-secret", "AWS_SESSION_TOKEN": "ambient-token"}):
            app, service, client = await self.application()
            data = bytes(range(256)) * 1024
            self.assertEqual((await client.put("/objects/file.bin", content=data)).status_code, 201)
            self.assertEqual((await client.get("/objects/file.bin")).content, data)
            stream, _incoming, outgoing = await exchange(app, path="/objects/slow.bin")
            self.assertEqual((await outgoing.get())["status"], 200)
            initial = (await outgoing.get())["body"]
            self.server.keys = dict([self.server.second])
            self.publish("b", self.server.values(self.server.second, SECOND_TOKEN))
            await service.coordinator.reload()
            self.assertEqual((await client.get("/objects/file.bin")).status_code, 401)
            headers = {"Authorization": "Bearer " + SECOND_TOKEN}
            self.assertEqual((await client.get("/objects/file.bin", headers=headers)).content, data)
            self.server.release.set()
            await stream
            received = bytearray(initial)
            while not outgoing.empty():
                received.extend((await outgoing.get()).get("body", b""))
            self.assertEqual(hashlib.sha256(received).digest(), hashlib.sha256(self.server.data).digest())
            await service.binding.wait_for_retirement(1)
            # Re-adopting the revoked key fails a real signed request. It cannot
            # fall back to ambient AWS credentials/session tokens.
            self.publish("c", self.server.values())
            with self.assertRaises(Exception):
                await service.coordinator.reload()
            self.assertFalse(service.ready())
            self.assertEqual(self.server.acks[-1]["status"], "failed")
            self.assertTrue(any(access is None for _method, _path, access in self.server.requests))
            self.assertEqual(self.server.puts, [("/assets/file.bin", data, self.server.first[0])])
            self.assertEqual(list(self.spool.iterdir()), [])

    async def test_stream_disconnect_and_same_revision_restore_close_captured_sockets(self):
        app, service, client = await self.application()
        stream, incoming, outgoing = await exchange(app, path="/objects/slow.bin")
        await outgoing.get()
        await outgoing.get()
        await incoming.put({"type": "http.disconnect"})
        await stream
        await asyncio.wait_for(self.server.stream_closed.wait(), 1)
        await service.restore()
        self.assertEqual(service.generation, 1)
        self.assertEqual([ack["revision"] for ack in self.server.acks], ["a" * 64, "a" * 64])
        self.assertEqual((await client.get("/objects/hello.bin")).content, b"hello\x00world")

    async def test_failed_and_timed_out_restore_remain_closed_until_recovery(self):
        _app, service, _client = await self.application()
        self.server.fail_list = True
        with self.assertRaises(StorageError):
            await service.restore()
        self.assertEqual(self.server.acks[-1]["status"], "failed")
        self.server.fail_list = False
        self.server.hold_list = True
        self.server.list_entered.clear()
        service._restore_timeout = 0.5
        before = len(self.server.acks)
        with self.assertRaises(StorageError):
            await service.restore()
        self.assertFalse(service.ready())
        self.assertTrue(self.server.list_entered.is_set())
        self.assertEqual(len(self.server.acks), before)
        self.server.hold_list = False
        self.server.list_release.set()
        # Constructing a real driver client can exceed a deliberately tiny test
        # budget on a loaded CI host. Recovery uses the production budget.
        service._restore_timeout = 1.5
        await service.restore()
        self.assertTrue(service.ready())

    async def test_truncated_stream_and_uncertain_put_are_not_replayed(self):
        _app, service, client = await self.application(transfer_timeout=0.15)
        with self.assertRaises(StorageError) as failure:
            await client.get("/objects/broken.bin")
        self.assertEqual(str(failure.exception), "storage_stream_failed")
        self.server.hold_put = True
        response = await client.put("/objects/uncertain.bin", content=b"write-once")
        self.assertEqual(response.status_code, 503)
        await service.binding.wait_for_retirement(1)
        await until(lambda: not service._active)
        self.assertEqual(self.server.puts, [("/assets/uncertain.bin", b"write-once", self.server.first[0])])
        self.assertEqual(sum(path == "/assets/broken.bin" for _method, path, _access in self.server.requests), 1)
        self.assertEqual(list(self.spool.iterdir()), [])
        self.server.put_release.set()
        self.assertEqual((await client.get("/objects/missing.bin")).status_code, 404)

    async def handler(self, **settings):
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        environment = {**os.environ, **self.env, "PORT": str(port), "TMPDIR": str(self.spool),
                       "PYTHONDONTWRITEBYTECODE": "1", **settings}
        process = await asyncio.create_subprocess_exec(sys.executable, "handler.py", cwd=Path(__file__).resolve().parents[1],
                                                        env=environment, stdout=asyncio.subprocess.PIPE,
                                                        stderr=asyncio.subprocess.STDOUT)

        async def cleanup():
            if process.returncode is None:
                process.terminate()
                try:
                    await asyncio.wait_for(process.wait(), 7)
                except TimeoutError:
                    process.kill()
                    await process.wait()

        self.addAsyncCleanup(cleanup)
        return process, f"http://127.0.0.1:{port}"

    async def test_actual_handler_startup_sighup_restore_and_shutdown(self):
        process, address = await self.handler()
        async with httpx.AsyncClient(base_url=address, timeout=5,
                                     headers={"Authorization": "Bearer " + TOKEN}) as client:
            async with asyncio.timeout(10):
                while True:
                    if process.returncode is not None:
                        self.fail("handler exited during startup")
                    try:
                        if (await client.get("/readyz")).status_code == 200:
                            break
                    except httpx.ConnectError:
                        pass
                    await asyncio.sleep(0.02)
            self.assertTrue(Path(self.env["FAAS_SECRETS_RELOAD_READY_FILE"]).exists())
            payload = bytes(range(256)) * 128
            self.assertEqual((await client.put("/objects/process.bin", content=payload)).status_code, 201)
            self.assertEqual((await client.get("/objects/process.bin")).content, payload)
            self.publish("b", self.server.values(self.server.second, SECOND_TOKEN))
            self.server.keys = dict([self.server.second])
            process.send_signal(signal.SIGHUP)
            await until(lambda: any(ack["revision"] == "b" * 64 and ack["status"] == "applied" for ack in self.server.acks))
            self.assertEqual((await client.get("/objects/process.bin")).status_code, 401)
            client.headers["Authorization"] = "Bearer " + SECOND_TOKEN
            self.assertEqual((await client.get("/objects/process.bin")).content, payload)
            before = len(self.server.acks)
            response = await client.post("/internal/after-restore", headers={"X-Faas-After-Restore": "1"})
            self.assertEqual(response.status_code, 204)
            self.assertEqual(len(self.server.acks), before + 1)
            self.assertEqual((await client.get("/objects/process.bin")).content, payload)
            self.server.fail_list = True
            self.assertEqual((await client.post("/internal/after-restore", headers={"X-Faas-After-Restore": "1"})).status_code, 503)
            self.assertEqual((await client.get("/readyz")).status_code, 503)
            self.server.fail_list = False
            self.assertEqual((await client.post("/internal/after-restore", headers={"X-Faas-After-Restore": "1"})).status_code, 204)
            # SIGTERM allows an admitted real HTTP body to finish before exit.
            async with client.stream("GET", "/objects/slow.bin") as response:
                iterator = response.aiter_bytes()
                initial = await anext(iterator)
                process.terminate()
                await asyncio.sleep(0.03)
                self.assertIsNone(process.returncode)
                self.server.release.set()
                result = initial + b"".join([chunk async for chunk in iterator])
                self.assertEqual(hashlib.sha256(result).digest(), hashlib.sha256(self.server.data).digest())
            # Uvicorn re-raises the captured termination signal after graceful
            # lifespan cleanup; both normal exit and SIGTERM status are valid.
            self.assertIn(await asyncio.wait_for(process.wait(), 7), (0, -signal.SIGTERM))
        output = (await process.communicate())[0].decode()
        self.assertNotIn("storage_cleanup_incomplete", output)
        for private in (TOKEN, SECOND_TOKEN, self.server.first[1], self.server.second[1], self.server.endpoint):
            self.assertNotIn(private, output)
        self.assertEqual(list(self.spool.iterdir()), [])
        self.assertTrue(all(ack["generation"] == "a" * 32 for ack in self.server.acks))
        self.assertEqual(json.loads(Path(self.env["FAAS_SECRETS_SNAPSHOT_FILE"]).read_text())["revision"], "b" * 64)
