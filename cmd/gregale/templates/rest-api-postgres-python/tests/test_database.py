"""Portable lifecycle tests; no database or SDK source checkout is needed."""

import asyncio
import importlib.abc
import json
import signal
import sys
import unittest
from contextlib import asynccontextmanager
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class NoRepositorySDK(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname == "faas_sdk" or fullname.startswith("faas_sdk."):
            raise AssertionError("Starter depends on a separately installed SDK")


sys.meta_path.insert(0, NoRepositorySDK())

import httpx
from app import create_app
from database import READINESS_SQL, Database, DatabaseError, connection_kwargs
from runtime.async_secret_reload_runtime import AsyncSecretReloadIO


def uri(password="private-first"):
    return f"postgresql://runtime:{password}@db.example:5432/app?sslmode=require"


class Cursor:
    def __init__(self, rows):
        self.rows = rows

    async def fetchall(self):
        return self.rows


class Pool:
    def __init__(self, owner, **options):
        self.owner, self.options = owner, options
        self.closed = self.commits = self.rollbacks = self.returned = 0
        self.connect_gate = self.query_gate = None
        self.ignore_cancel = False
        self.entered = asyncio.Event()

    async def open(self):
        pass

    async def close(self, **_options):
        self.closed += 1

    async def pause(self, gate):
        if gate is not None:
            self.entered.set()
            try:
                await gate.wait()
            except asyncio.CancelledError:
                if not self.ignore_cancel:
                    raise
                await gate.wait()

    @asynccontextmanager
    async def connection(self, **_options):
        await self.pause(self.connect_gate)
        try:
            yield self
        finally:
            self.returned += 1

    @asynccontextmanager
    async def transaction(self):
        try:
            yield
        except BaseException:
            self.rollbacks += 1
            raise
        else:
            self.commits += 1

    async def execute(self, sql, parameters=()):
        if sql == READINESS_SQL:
            await self.pause(self.owner.validate_gate)
        elif sql == "blocked-write" or sql.startswith("INSERT"):
            await self.pause(self.query_gate)
        if self.owner.bad_validation and sql == READINESS_SQL:
            raise RuntimeError(uri())
        return Cursor([{"id": 1, "body": "hello"}] if "RETURNING" in sql else [])


class Harness:
    def __init__(self):
        self.env = {
            "FAAS_SECRETS_SNAPSHOT_FILE": "/run/secrets.json",
            "FAAS_SECRETS_RELOAD_READY_FILE": "/run/secrets.ready",
            "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": "http://127.0.0.1:4100/ack",
            "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32,
        }
        self.snapshot = {"revision": "a" * 64, "secrets": {"DATABASE_URL": uri()}}
        self.pools, self.acks = [], []
        self.validate_gate = None
        self.bad_validation = False
        self.ignore_cancel = False

    def factory(self, **options):
        pool = Pool(self, **options)
        pool.ignore_cancel = self.ignore_cancel
        self.pools.append(pool)
        return pool

    async def read(self, _path, cancel):
        cancel.check()
        return json.dumps(self.snapshot)

    async def ready(self, _path, cancel):
        cancel.check()

    async def post(self, _endpoint, body, cancel):
        cancel.check()
        self.acks.append(json.loads(body))
        return 202

    def database(self, **options):
        return Database(env=self.env, io=AsyncSecretReloadIO(read=self.read, ready=self.ready, post=self.post),
                        pool_factory=self.factory, **options)

    async def validation_entered(self):
        async with asyncio.timeout(1):
            while not self.pools[-1].entered.is_set():
                await asyncio.sleep(0)


class DatabaseTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
        self.h = Harness()
        self.db = self.h.database(restore_timeout=0.05)

    async def asyncTearDown(self):
        self.h.validate_gate = None
        await self.db.close()
        signal.signal(signal.SIGHUP, self.previous)

    async def test_startup_snapshot_validation_and_migration_isolation(self):
        await self.db.start()
        self.assertEqual(self.h.acks[0]["status"], "applied")
        self.assertEqual(self.h.pools[0].options["kwargs"]["sslmode"], "verify-full")
        self.h.snapshot["secrets"]["MIGRATION_DATABASE_URL"] = uri("migration")
        with self.assertRaises(Exception):
            await self.db.reload()
        self.assertEqual(len(self.h.pools), 1)
        self.assertEqual(self.h.acks[-1]["status"], "failed")
        with self.assertRaises(DatabaseError):
            await self.db.query(READINESS_SQL)
        with self.assertRaises(DatabaseError):
            Database(env={**self.h.env, "MIGRATION_DATABASE_URL": uri()})
        with self.assertRaises(Exception):
            Database(env={"DATABASE_URL": uri()})

    async def test_rotation_preserves_a_leased_transaction_and_revocation_blocks_new_work(self):
        await self.db.start()
        old = self.h.pools[0]
        old.query_gate = asyncio.Event()
        query = asyncio.create_task(self.db.query("blocked-write", write=True))
        await old.entered.wait()
        self.h.snapshot = {"revision": "b" * 64, "secrets": {"DATABASE_URL": uri("second")}}
        await self.db.reload()
        self.assertEqual(old.closed, 0)
        self.h.snapshot = {"revision": "c" * 64, "secrets": {}}
        with self.assertRaises(Exception):
            await self.db.reload()
        with self.assertRaises(DatabaseError):
            await self.db.query(READINESS_SQL)
        old.query_gate.set()
        await query
        await self.db.binding.wait_for_retirement(1)
        self.assertEqual(old.commits, 2)  # Initial validation and the one admitted write.
        self.assertEqual(old.closed, 1)

    async def test_restore_replaces_same_revision_pool_cancels_captured_work_and_coalesces(self):
        await self.db.start()
        old = self.h.pools[0]
        old.query_gate = asyncio.Event()
        query = asyncio.create_task(self.db.query("blocked-write", write=True))
        await old.entered.wait()
        self.h.validate_gate = asyncio.Event()
        first = asyncio.create_task(self.db.restore())
        await asyncio.sleep(0)
        second = asyncio.create_task(self.db.restore())
        await self.h.validation_entered()
        with self.assertRaises(DatabaseError):
            await self.db.query(READINESS_SQL)
        self.h.validate_gate.set()
        await asyncio.gather(first, second)
        with self.assertRaises(DatabaseError):
            await query
        self.assertEqual(self.db.generation, 1)
        self.assertEqual(len(self.h.pools), 2)
        self.assertEqual(old.closed, 1)
        self.assertEqual(old.rollbacks, 1)
        self.assertEqual([a["revision"] for a in self.h.acks], ["a" * 64, "a" * 64])
        await self.db.query(READINESS_SQL)

    async def test_timed_out_validator_cannot_publish_or_ack_and_later_restore_recovers(self):
        await self.db.start()
        self.h.validate_gate = asyncio.Event()
        self.h.ignore_cancel = True
        restore = asyncio.create_task(self.db.restore())
        await self.h.validation_entered()
        with self.assertRaises(DatabaseError):
            await restore
        self.assertEqual(len(self.h.acks), 1)
        with self.assertRaises(DatabaseError):
            await self.db.query(READINESS_SQL)
        self.h.validate_gate.set()
        await self.db.binding.wait_for_retirement(1)
        self.assertEqual(self.h.pools[-1].closed, 1)
        self.h.validate_gate = None
        await self.db.restore()
        self.assertEqual(self.db.generation, 1)
        await self.db.query(READINESS_SQL)

    async def test_restore_queued_behind_rotation_times_out_without_late_adoption(self):
        await self.db.start()
        self.h.validate_gate = asyncio.Event()
        self.h.ignore_cancel = True
        rotation = asyncio.create_task(self.db.reload())
        await self.h.validation_entered()
        with self.assertRaises(DatabaseError):
            await self.db.restore()
        self.assertEqual(len(self.h.acks), 1)
        self.h.validate_gate.set()
        await asyncio.gather(rotation, return_exceptions=True)
        await self.db.binding.wait_for_retirement(1)
        self.assertEqual(len(self.h.acks), 1)
        self.h.validate_gate = None
        await self.db.restore()

    async def test_cancelled_late_connection_retains_lease_until_return(self):
        await self.db.start()
        old = self.h.pools[0]
        old.connect_gate = asyncio.Event()
        old.ignore_cancel = True
        query = asyncio.create_task(self.db.query(READINESS_SQL))
        await old.entered.wait()
        query.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await query
        self.assertEqual(old.closed, 0)
        with self.assertRaises(DatabaseError):
            await self.db.restore()
        self.assertEqual(old.closed, 0)
        old.connect_gate.set()
        await self.db.binding.wait_for_retirement(1)
        self.assertEqual(old.returned, 2)
        self.assertEqual(old.closed, 1)
        await self.db.restore()

    async def test_http_readiness_and_restore_peer_authorization(self):
        app = create_app(lambda: self.db)
        async with app.router.lifespan_context(app):
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app, client=("198.51.100.2", 1234)),
                                        base_url="http://example") as client:
                self.assertEqual((await client.get("/healthz")).status_code, 200)
                self.assertEqual((await client.post("/notes", json={"body": "hello"})).status_code, 201)
                self.assertEqual((await client.post("/notes", json={"body": ""})).status_code, 400)
                self.assertEqual((await client.post("/internal/after-restore", headers={
                    "X-Faas-After-Restore": "1", "X-Forwarded-For": "127.0.0.1"})).status_code, 403)
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app, client=("127.0.0.1", 1)),
                                        base_url="http://example") as client:
                self.assertEqual((await client.post("/internal/after-restore")).status_code, 403)
                self.assertEqual((await client.post("/internal/after-restore", headers={
                    "X-Faas-After-Restore": "1"})).status_code, 204)
                self.h.bad_validation = True
                self.assertEqual((await client.post("/internal/after-restore", headers={
                    "X-Faas-After-Restore": "1"})).status_code, 503)
                self.assertEqual((await client.get("/healthz")).status_code, 503)

    async def test_cancelled_restore_caller_is_fenced_and_does_not_stick_the_gate(self):
        await self.db.start()
        self.h.validate_gate = asyncio.Event()
        self.h.ignore_cancel = True
        task = asyncio.create_task(self.db.restore())
        await self.h.validation_entered()
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.h.validate_gate.set()
        await self.db.binding.wait_for_retirement(1)
        self.assertEqual(len(self.h.acks), 1)
        self.h.validate_gate = None
        await self.db.restore()
        self.assertEqual(self.db.generation, 1)

    async def test_http_disconnect_rolls_back_late_write_before_releasing_lease(self):
        app = create_app(lambda: self.db)
        async with app.router.lifespan_context(app):
            old = self.h.pools[0]
            old.query_gate = asyncio.Event()
            old.ignore_cancel = True
            messages, responses = asyncio.Queue(), []
            await messages.put({"type": "http.request", "body": b'{"body":"cancelled"}', "more_body": False})
            async def send(message):
                responses.append(message)
            scope = {"type": "http", "asgi": {"version": "3.0"}, "http_version": "1.1", "method": "POST",
                     "scheme": "http", "path": "/notes", "raw_path": b"/notes", "query_string": b"",
                     "root_path": "", "headers": [(b"content-type", b"application/json")],
                     "client": ("198.51.100.2", 1234), "server": ("example", 80)}
            request = asyncio.create_task(app(scope, messages.get, send))
            await old.entered.wait()
            await messages.put({"type": "http.disconnect"})
            await asyncio.wait_for(request, 1)
            self.assertEqual(responses[0]["status"], 503)
            self.assertEqual(old.closed, 0)
            with self.assertRaises(DatabaseError):
                await self.db.restore()
            old.query_gate.set()
            await self.db.binding.wait_for_retirement(1)
            self.assertEqual(old.closed, 1)
            self.assertEqual(old.commits, 1)  # Only startup validation committed.
            self.assertEqual(old.rollbacks, 1)

    async def test_close_during_restore_drains_failed_recovery_and_is_terminal(self):
        await self.db.start()
        self.h.validate_gate = asyncio.Event()
        task = asyncio.create_task(self.db.restore())
        await self.h.validation_entered()
        await self.db.close()
        await asyncio.gather(task, return_exceptions=True)
        self.assertTrue(all(pool.closed == 1 for pool in self.h.pools))
        with self.assertRaises(DatabaseError):
            await self.db.restore()
        with self.assertRaises(DatabaseError):
            await self.db.query(READINESS_SQL)


class ConfigurationTests(unittest.TestCase):
    def test_tls_is_verified_and_insecure_modes_and_incomplete_identity_are_rejected(self):
        self.assertEqual(connection_kwargs(uri())["sslrootcert"], "system")
        self.assertEqual(connection_kwargs(uri() + "&sslrootcert=/run/private-ca.crt")["sslrootcert"],
                         "/run/private-ca.crt")
        for value in [uri().replace("require", "disable"), "postgresql:///ambient", "", None]:
            with self.assertRaises(Exception):
                connection_kwargs(value)


if __name__ == "__main__":
    unittest.main()
