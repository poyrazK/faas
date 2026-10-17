"""Opt-in acceptance against a disposable TLS/SCRAM superuser database."""

import asyncio
import json
import os
import secrets
import signal
import sys
import tempfile
import unittest
from pathlib import Path
from urllib.parse import quote, urlsplit, urlunsplit

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import httpx
import psycopg
from app import create_app
from database import READINESS_SQL, Database
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict
from psycopg_pool import AsyncConnectionPool


@unittest.skipUnless(os.environ.get("GREGALE_PYTHON_POSTGRES_TEST") == "1", "requires disposable TLS/SCRAM PostgreSQL")
class PostgresTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        self.acks, self.pools, self.handlers = [], [], set()
        self.server = await asyncio.start_server(self.ack, "127.0.0.1", 0)
        self.env = {
            "FAAS_SECRETS_SNAPSHOT_FILE": str(self.root / "snapshot.json"),
            "FAAS_SECRETS_RELOAD_READY_FILE": str(self.root / "ready"),
            "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": f"http://127.0.0.1:{self.server.sockets[0].getsockname()[1]}/ack",
            "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32,
        }
        self.admin_uri = os.environ["DATABASE_URL"]
        self.assertEqual(conninfo_to_dict(self.admin_uri).get("sslmode"), "verify-full")
        self.admin = await psycopg.AsyncConnection.connect(self.admin_uri, autocommit=True)
        suffix = secrets.token_hex(5)
        self.name, self.runtime, self.migration = "app_" + suffix, "run_" + suffix, "mig_" + suffix
        self.first, self.second, self.migration_password = (secrets.token_hex(20) for _ in range(3))
        for role, password in [(self.runtime, self.first), (self.migration, self.migration_password)]:
            await self.admin.execute(sql.SQL("CREATE ROLE {} LOGIN PASSWORD {}").format(sql.Identifier(role), sql.Literal(password)))
        await self.admin.execute(sql.SQL("CREATE DATABASE {} OWNER {}").format(sql.Identifier(self.name), sql.Identifier(self.migration)))
        self.database_admin = await psycopg.AsyncConnection.connect(self.uri(), autocommit=True)
        await self.migrate()
        self.publish("a", self.uri(self.runtime, self.first))

    def uri(self, role=None, password=None):
        parsed = urlsplit(self.admin_uri)
        host = f"[{parsed.hostname}]" if ":" in parsed.hostname else parsed.hostname
        netloc = (f"{quote(role)}:{quote(password)}@{host}:{parsed.port}" if role else parsed.netloc)
        return urlunsplit(parsed._replace(netloc=netloc, path="/" + self.name))

    async def migrate(self):
        process = await asyncio.create_subprocess_exec(sys.executable, "migrate.py", env={
            **os.environ, "MIGRATION_DATABASE_URL": self.uri(self.migration, self.migration_password)},
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        output, error = await process.communicate()
        self.assertEqual(process.returncode, 0, (output + error).decode())
        await self.database_admin.execute(sql.SQL("GRANT USAGE ON SCHEMA public TO {}").format(sql.Identifier(self.runtime)))
        await self.database_admin.execute(sql.SQL("GRANT SELECT, INSERT ON public.notes TO {}").format(sql.Identifier(self.runtime)))
        await self.database_admin.execute(sql.SQL("GRANT USAGE ON SEQUENCE public.notes_id_seq TO {}").format(sql.Identifier(self.runtime)))

    def publish(self, revision, uri):
        file = self.root / "next.json"
        file.write_text(json.dumps({"revision": revision * 64, "secrets": {} if uri is None else {"DATABASE_URL": uri}}))
        file.chmod(0o600)
        os.replace(file, self.env["FAAS_SECRETS_SNAPSHOT_FILE"])

    async def ack(self, reader, writer):
        task = asyncio.current_task()
        self.handlers.add(task)
        try:
            async with asyncio.timeout(5):
                headers = (await reader.readuntil(b"\r\n\r\n")).decode().split("\r\n")
                length = int(next(line.split(":", 1)[1] for line in headers if line.lower().startswith("content-length:")))
                self.acks.append(json.loads(await reader.readexactly(length)))
                writer.write(b"HTTP/1.1 202 Accepted\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()
            self.handlers.discard(task)

    def database(self):
        def factory(**options):
            pool = AsyncConnectionPool(**options)
            self.pools.append(pool)
            return pool
        self.db = Database(env=self.env, pool_factory=factory)
        return self.db

    async def restore(self, client):
        return await client.post("/internal/after-restore", headers={"X-Faas-After-Restore": "1"})

    async def wait_for_blocked_validator(self):
        async with asyncio.timeout(3):
            while True:
                cursor = await self.database_admin.execute(
                    "SELECT count(*) FROM pg_stat_activity WHERE usename = %s AND wait_event_type = 'Lock' AND query = %s",
                    (self.runtime, READINESS_SQL))
                if (await cursor.fetchone())[0]:
                    return
                await asyncio.sleep(0.005)

    async def asyncTearDown(self):
        try:
            if hasattr(self, "db"):
                await self.db.close()
            await self.database_admin.close()
            await self.admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(self.name)))
            for role in [self.runtime, self.migration]:
                await self.admin.execute(sql.SQL("DROP ROLE {}").format(sql.Identifier(role)))
            await self.admin.close()
            self.server.close()
            await self.server.wait_closed()
            await asyncio.gather(*self.handlers, return_exceptions=True)
            self.assertTrue(all(pool.closed for pool in self.pools))
        finally:
            self.directory.cleanup()
            signal.signal(signal.SIGHUP, self.previous)

    async def test_rotation_revocation_stale_sessions_and_schema_recovery(self):
        app = create_app(self.database)
        async with app.router.lifespan_context(app):
            self.assertEqual(self.acks[0]["status"], "applied")
            self.assertEqual(Path(self.env["FAAS_SECRETS_RELOAD_READY_FILE"]).read_text(), "ready\n")
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app, client=("127.0.0.1", 1)), base_url="http://example") as client:
                self.assertEqual((await client.get("/healthz")).status_code, 200)
                async with self.db.binding.acquire() as lease:
                    old = lease.current()
                    async with old.connection() as connection:
                        await connection.execute("INSERT INTO public.notes (body) VALUES ('admitted-once')")
                        await self.admin.execute(sql.SQL("ALTER ROLE {} PASSWORD {}").format(sql.Identifier(self.runtime), sql.Literal(self.second)))
                        with self.assertRaises(psycopg.OperationalError):
                            await psycopg.AsyncConnection.connect(self.uri(self.runtime, self.first), connect_timeout=2)
                        self.publish("b", self.uri(self.runtime, self.second))
                        os.kill(os.getpid(), signal.SIGHUP)
                        async with asyncio.timeout(5):
                            while len(self.acks) < 2:
                                await asyncio.sleep(0.005)
                        self.assertFalse(old.closed)
                        self.assertEqual((await client.post("/notes", json={"body": "replacement"})).status_code, 201)
                        self.publish("c", None)
                        with self.assertRaises(Exception):
                            await self.db.reload()
                        self.assertEqual((await client.get("/healthz")).status_code, 503)
                    self.assertFalse(old.closed)
                await self.db.binding.wait_for_retirement(5)
                self.assertTrue(old.closed)
                cursor = await self.database_admin.execute("SELECT count(*) FROM public.notes")
                self.assertEqual((await cursor.fetchone())[0], 2)
                self.publish("d", self.uri(self.runtime, self.second))
                self.assertEqual((await self.restore(client)).status_code, 204)
                current = self.pools[-1]
                async with current.connection() as connection:
                    pid = connection.info.backend_pid
                await self.database_admin.execute("SELECT pg_terminate_backend(%s)", (pid,))
                self.assertEqual((await self.restore(client)).status_code, 204)
                self.assertTrue(current.closed)
                self.assertEqual((await client.get("/healthz")).status_code, 200)
                async with self.pools[-1].connection() as connection:
                    with self.assertRaises(psycopg.errors.InsufficientPrivilege):
                        await connection.execute("CREATE TABLE public.forbidden (id int)")
                await self.database_admin.execute("DROP TABLE public.notes")
                self.assertEqual((await self.restore(client)).status_code, 503)
                self.assertEqual((await client.get("/healthz")).status_code, 503)
                await self.migrate()
                self.assertEqual((await self.restore(client)).status_code, 204)
                self.assertEqual((await client.get("/healthz")).status_code, 200)

    async def test_restore_queued_and_active_validation_timeouts_are_fenced(self):
        app = create_app(self.database)
        async with app.router.lifespan_context(app):
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app, client=("127.0.0.1", 1)), base_url="http://example") as client:
                lock = await psycopg.AsyncConnection.connect(self.uri())
                try:
                    await lock.execute("LOCK TABLE public.notes IN ACCESS EXCLUSIVE MODE")
                    self.publish("b", self.uri(self.runtime, self.first))
                    os.kill(os.getpid(), signal.SIGHUP)
                    await self.wait_for_blocked_validator()
                    self.assertEqual((await self.restore(client)).status_code, 503)
                    self.assertEqual((await client.get("/healthz")).status_code, 503)
                    self.assertEqual(len(self.acks), 1)
                    await lock.rollback()
                    await self.db.binding.wait_for_retirement(5)
                    self.publish("c", self.uri(self.runtime, self.first))
                    self.assertEqual((await self.restore(client)).status_code, 204)
                    await lock.execute("LOCK TABLE public.notes IN ACCESS EXCLUSIVE MODE")
                    restore = asyncio.create_task(self.restore(client))
                    await self.wait_for_blocked_validator()
                    self.assertEqual((await restore).status_code, 503)
                    self.assertEqual(len(self.acks), 2)
                    await lock.rollback()
                    await self.db.binding.wait_for_retirement(5)
                    self.assertEqual(len(self.acks), 2)
                    self.assertEqual((await self.restore(client)).status_code, 204)
                    self.assertEqual((await client.get("/healthz")).status_code, 200)
                finally:
                    await lock.close()

    async def test_real_handler_startup_sighup_restore_and_shutdown(self):
        listener = await asyncio.start_server(lambda _reader, writer: writer.close(), "127.0.0.1", 0)
        port = listener.sockets[0].getsockname()[1]
        listener.close()
        await listener.wait_closed()
        environment = {key: value for key, value in os.environ.items() if not key.startswith("FAAS_SECRETS_")
                       and key != "MIGRATION_DATABASE_URL"}
        environment.update(self.env, PORT=str(port))
        with (self.root / "handler.log").open("wb") as log:
            process = await asyncio.create_subprocess_exec(sys.executable, "handler.py", env=environment,
                                                           stdout=log, stderr=log)
            try:
                async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{port}", trust_env=False, timeout=2) as client:
                    async with asyncio.timeout(10):
                        while True:
                            if process.returncode is not None:
                                self.fail("handler exited before readiness")
                            try:
                                if (await client.get("/healthz")).status_code == 200:
                                    break
                            except httpx.TransportError:
                                pass
                            await asyncio.sleep(0.02)
                    self.assertEqual(self.acks[0]["status"], "applied")
                    self.publish("b", None)
                    process.send_signal(signal.SIGHUP)
                    async with asyncio.timeout(5):
                        while len(self.acks) < 2:
                            await asyncio.sleep(0.005)
                    self.assertEqual(self.acks[-1]["status"], "failed")
                    self.assertEqual((await client.get("/healthz")).status_code, 503)
                    self.assertEqual((await self.restore(client)).status_code, 503)
                    self.publish("c", self.uri(self.runtime, self.first))
                    response = await client.post("/internal/after-restore", headers={
                        "X-Faas-After-Restore": "1", "X-Forwarded-For": "198.51.100.2"})
                    self.assertEqual(response.status_code, 204)  # Actual loopback peer, forwarded header ignored.
                    self.assertEqual((await client.get("/healthz")).status_code, 200)
            finally:
                if process.returncode is None:
                    process.terminate()
                try:
                    await asyncio.wait_for(process.wait(), 10)
                except TimeoutError:
                    process.kill()
                    await process.wait()
                    raise
            self.assertIn(process.returncode, (0, -signal.SIGTERM))
        cursor = await self.database_admin.execute("SELECT count(*) FROM pg_stat_activity WHERE usename = %s", (self.runtime,))
        self.assertEqual((await cursor.fetchone())[0], 0)


if __name__ == "__main__":
    unittest.main()
