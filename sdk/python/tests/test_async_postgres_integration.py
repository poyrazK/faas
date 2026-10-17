"""Opt-in TLS/SCRAM acceptance for the exact async Python example."""

import asyncio
import importlib.util
import json
import os
import secrets
import signal
import sys
from contextlib import asynccontextmanager
from pathlib import Path
from urllib.parse import quote, urlsplit, urlunsplit

import httpx
import pytest

pytestmark = pytest.mark.skipif(
    os.environ.get("GREGALE_ASYNC_POSTGRES_TEST") != "1",
    reason="requires an explicitly selected disposable TLS/SCRAM PostgreSQL database",
)


@asynccontextmanager
async def database():
    psycopg = pytest.importorskip("psycopg")
    from psycopg import sql
    from psycopg.conninfo import conninfo_to_dict

    admin_uri = os.environ["DATABASE_URL"]
    assert conninfo_to_dict(admin_uri).get("sslmode") == "verify-full", "fixture must verify TLS"
    role, table = "async_binding_" + secrets.token_hex(6), "async_work_" + secrets.token_hex(6)
    first, second = secrets.token_hex(20), secrets.token_hex(20)
    admin = await psycopg.AsyncConnection.connect(admin_uri, autocommit=True)
    parsed = urlsplit(admin_uri)
    host = f"[{parsed.hostname}]" if ":" in parsed.hostname else parsed.hostname

    def uri(password):
        return urlunsplit(parsed._replace(netloc=f"{quote(role)}:{quote(password)}@{host}:{parsed.port}"))

    try:
        await admin.execute(
            sql.SQL("CREATE ROLE {} LOGIN PASSWORD {}").format(sql.Identifier(role), sql.Literal(first))
        )
        await admin.execute(sql.SQL("CREATE TABLE {} (id text PRIMARY KEY)").format(sql.Identifier(table)))
        await admin.execute(
            sql.SQL("GRANT SELECT, INSERT ON {} TO {}").format(sql.Identifier(table), sql.Identifier(role))
        )
        yield admin, role, table, uri(first), uri(second), second
    finally:
        await admin.execute(sql.SQL("DROP TABLE IF EXISTS {}").format(sql.Identifier(table)))
        await admin.execute(sql.SQL("DROP ROLE IF EXISTS {}").format(sql.Identifier(role)))
        await admin.close()


@asynccontextmanager
async def acknowledgement_server():
    acks, writers, tasks = [], set(), set()

    async def handle(reader, writer):
        task = asyncio.current_task()
        writers.add(writer)
        tasks.add(task)
        try:
            async with asyncio.timeout(5):
                headers = (await reader.readuntil(b"\r\n\r\n")).decode().split("\r\n")
                size = int(
                    next(line.split(":", 1)[1] for line in headers if line.lower().startswith("content-length:"))
                )
                acks.append(json.loads(await reader.readexactly(size)))
                writer.write(b"HTTP/1.1 202 Accepted\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()
            writers.discard(writer)
            tasks.discard(task)

    server = await asyncio.start_server(handle, "127.0.0.1", 0)
    try:
        yield f"http://127.0.0.1:{server.sockets[0].getsockname()[1]}/internal/secrets/ack", acks
    finally:
        server.close()
        await server.wait_closed()
        for writer in tuple(writers):
            writer.close()
        await asyncio.gather(*tasks, return_exceptions=True)


def load_example(monkeypatch):
    pytest.importorskip("fastapi")
    pytest.importorskip("psycopg_pool")
    example = Path(__file__).resolve().parents[3] / "examples/postgres-pool-rotation-python"
    for name in ("database", "app"):
        spec = importlib.util.spec_from_file_location(name, example / f"{name}.py")
        module = importlib.util.module_from_spec(spec)
        monkeypatch.setitem(sys.modules, name, module)
        spec.loader.exec_module(module)
    return module


def project(tmp_path, endpoint, uri):
    env = {
        "FAAS_SECRETS_SNAPSHOT_FILE": str(tmp_path / "snapshot.json"),
        "FAAS_SECRETS_RELOAD_READY_FILE": str(tmp_path / "ready"),
        "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": endpoint,
        "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32,
    }

    def publish(revision, values):
        temporary = tmp_path / "next.json"
        temporary.write_text(json.dumps({"revision": revision * 64, "secrets": values}))
        temporary.chmod(0o600)
        os.replace(temporary, env["FAAS_SECRETS_SNAPSHOT_FILE"])

    publish("a", {"DATABASE_URL": uri})
    return env, publish


async def wait_for_sleep(admin, pid):
    async with asyncio.timeout(3):
        while True:
            cursor = await admin.execute("SELECT wait_event FROM pg_stat_activity WHERE pid = %s", (pid,))
            row = await cursor.fetchone()
            if row and row[0] == "PgSleep":
                return
            await asyncio.sleep(0.005)


@pytest.fixture(autouse=True)
def own_signal():
    previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
    yield
    signal.signal(signal.SIGHUP, previous)


def test_real_asgi_rotation_revocation_and_open_transaction(tmp_path, monkeypatch):
    example = load_example(monkeypatch)

    async def scenario():
        import psycopg
        from psycopg import sql

        async with database() as (admin, role, table, first_uri, second_uri, second):
            async with acknowledgement_server() as (endpoint, acks):
                env, publish = project(tmp_path, endpoint, first_uri)
                app = example.create_app(env=env)
                async with app.router.lifespan_context(app):
                    assert Path(env["FAAS_SECRETS_RELOAD_READY_FILE"]).read_text() == "ready\n"
                    assert acks == [{"revision": "a" * 64, "status": "applied", "generation": "a" * 32}]
                    binding, coordinator = app.state.database, app.state.coordinator
                    async with httpx.AsyncClient(
                        transport=httpx.ASGITransport(app), base_url="http://example"
                    ) as client:
                        assert (await client.get("/db-time")).status_code == 200
                        async with binding.acquire() as lease:
                            old = lease.current()
                            async with old.connection(timeout=3) as connection:
                                await connection.execute(
                                    sql.SQL("INSERT INTO {} VALUES (%s)").format(sql.Identifier(table)),
                                    ("exactly-once",),
                                )
                                await admin.execute(
                                    sql.SQL("ALTER ROLE {} PASSWORD {}").format(
                                        sql.Identifier(role), sql.Literal(second)
                                    )
                                )
                                with pytest.raises(psycopg.OperationalError):
                                    await psycopg.AsyncConnection.connect(first_uri, connect_timeout=2)
                                publish("b", {"DATABASE_URL": second_uri})
                                os.kill(os.getpid(), signal.SIGHUP)
                                async with asyncio.timeout(5):
                                    while len(acks) < 2:
                                        await asyncio.sleep(0.005)
                                assert acks[-1]["revision"] == "b" * 64 and acks[-1]["status"] == "applied"
                                assert not old.closed
                                assert (await client.get("/db-time")).status_code == 200
                                publish("c", {})
                                from faas_sdk.secret_reload_runtime import SecretReloadError

                                with pytest.raises(SecretReloadError):
                                    await coordinator.reload()
                                assert acks[-1]["status"] == "failed"
                                assert (await client.get("/db-time")).status_code == 503
                                assert (await client.get("/healthz")).status_code == 200
                            assert not old.closed  # Connection returned; lease still owns pool.
                        await binding.wait_for_retirement(5)
                        assert old.closed
                        cursor = await admin.execute(sql.SQL("SELECT count(*) FROM {}").format(sql.Identifier(table)))
                        assert (await cursor.fetchone())[0] == 1
                        publish("d", {"DATABASE_URL": second_uri})
                        await coordinator.reload()
                        assert (await client.get("/db-time")).status_code == 200
                assert signal.getsignal(signal.SIGHUP) == signal.SIG_IGN

    asyncio.run(scenario())


def test_real_validator_and_transaction_cancellation_leave_no_lease_or_ack(tmp_path, monkeypatch):
    example = load_example(monkeypatch)

    async def scenario():
        from faas_sdk.secret_reload_runtime import SecretReloadError

        async with database() as (admin, _role, _table, first_uri, _second_uri, _second):
            async with acknowledgement_server() as (endpoint, acks):
                env, _publish = project(tmp_path, endpoint, first_uri)
                app = example.create_app(env=env)
                async with app.router.lifespan_context(app):
                    binding, coordinator = app.state.database, app.state.coordinator
                    entered = asyncio.Event()
                    cancel = asyncio.Event()
                    backend = []
                    original_validate = binding._validate

                    async def blocked_validate(pool, cancel):
                        async with pool.connection(timeout=3) as connection:
                            backend.append(connection.info.backend_pid)
                            entered.set()
                            await connection.execute("SELECT pg_sleep(10)")
                        cancel.check()

                    binding._validate = blocked_validate
                    task = asyncio.create_task(coordinator.reload(timeout=5, cancel=cancel))
                    async with asyncio.timeout(5):
                        await entered.wait()
                    await wait_for_sleep(admin, backend[-1])
                    async with httpx.AsyncClient(
                        transport=httpx.ASGITransport(app), base_url="http://example"
                    ) as client:
                        assert (await client.get("/healthz")).status_code == 200
                    cancel.set()
                    with pytest.raises(SecretReloadError):
                        await task
                    await binding.wait_for_retirement(5)
                    assert len(acks) == 1  # Cancelled validation produced no adoption evidence.
                    binding._validate = original_validate
                    await coordinator.reload()
                    entered.clear()

                    async def transaction():
                        async with binding.acquire() as lease:
                            async with lease.current().connection(timeout=3) as connection:
                                backend.append(connection.info.backend_pid)
                                entered.set()
                                await connection.execute("SELECT pg_sleep(10)")

                    task = asyncio.create_task(transaction())
                    async with asyncio.timeout(5):
                        await entered.wait()
                    await wait_for_sleep(admin, backend[-1])
                    task.cancel()
                    with pytest.raises(asyncio.CancelledError):
                        await task
                    binding.invalidate()
                    await binding.wait_for_retirement(5)

    asyncio.run(scenario())
