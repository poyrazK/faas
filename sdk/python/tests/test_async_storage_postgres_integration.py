"""Mixed adoption against real TLS/SCRAM PostgreSQL and signed local S3."""

import asyncio
import hashlib
import os
import secrets
import signal
from contextlib import asynccontextmanager
from urllib.parse import quote, urlsplit, urlunsplit

import pytest

pytest.importorskip("aiobotocore")
pytest.importorskip("psycopg_pool")
pytest.importorskip("fastapi")

import psycopg
from async_storage_fixture import SignedS3, finish_stream, load_example, open_stream, projection
from botocore.exceptions import ClientError
from faas_sdk.preview.async_postgres_pool_runtime import AsyncPostgresPoolBinding
from faas_sdk.preview.object_storage_runtime import ObjectStorageBindingError
from faas_sdk.preview.postgres_pool_runtime import PostgresPoolBindingError
from faas_sdk.secret_reload_runtime import SecretReloadError
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict
from psycopg_pool import AsyncConnectionPool

pytestmark = pytest.mark.skipif(
    os.environ.get("GREGALE_ASYNC_STORAGE_POSTGRES_TEST") != "1",
    reason="requires an explicitly selected disposable TLS/SCRAM superuser database",
)


@asynccontextmanager
async def postgres():
    admin_uri = os.environ["DATABASE_URL"]
    assert conninfo_to_dict(admin_uri).get("sslmode") == "verify-full"
    suffix = secrets.token_hex(6)
    role, table = "async_files_" + suffix, "async_files_work_" + suffix
    first, second = secrets.token_hex(20), secrets.token_hex(20)
    admin = await psycopg.AsyncConnection.connect(admin_uri, autocommit=True)
    parsed = urlsplit(admin_uri)
    host = f"[{parsed.hostname}]" if ":" in parsed.hostname else parsed.hostname

    def uri(password):
        return urlunsplit(parsed._replace(netloc=f"{quote(role)}:{password}@{host}:{parsed.port}"))

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


def make_postgres_binding():
    pools = []

    async def factory(config, cancel):
        cancel.check()
        pool = AsyncConnectionPool(
            config.connection_string,
            open=False,
            min_size=0,
            max_size=3,
            timeout=3,
            kwargs={"connect_timeout": 3, "options": "-c statement_timeout=3000"},
        )
        try:
            await pool.open()
            cancel.check()
            pools.append(pool)
            return pool
        except BaseException:
            await pool.close(timeout=3)
            raise

    async def validate(pool, cancel):
        cancel.check()
        async with pool.connection(timeout=3) as connection:
            await connection.execute("SELECT 1")
        cancel.check()

    async def retire(pool):
        await pool.close(timeout=3)

    return AsyncPostgresPoolBinding("DATABASE_URL", factory, validate=validate, retire=retire), pools


def test_real_mixed_adoption_rotation_failure_cancellation_and_stream_draining(tmp_path, monkeypatch):
    storage_module, example = load_example(monkeypatch)

    async def scenario():
        async with postgres() as (admin, role, table, first_uri, second_uri, password):
            async with SignedS3() as server:
                database, pools = make_postgres_binding()
                storage = storage_module.make_binding()
                created, retired = [], []
                factory, retire = storage._factory, storage._retire

                async def make(config, cancel):
                    owned = await factory(config, cancel)
                    created.append(owned)
                    return owned

                async def close(owned):
                    await retire(owned)
                    retired.append(owned)

                storage._factory, storage._retire = make, close
                env, publish = projection(tmp_path, server, {**server.values(), "DATABASE_URL": first_uri})
                app = example.create_app(env=env, binding_factory=lambda: storage, extra_bindings=[database])
                async with app.router.lifespan_context(app):
                    assert [ack["status"] for ack in server.acks] == ["applied"]
                    async with database.acquire() as lease:
                        old_pool = lease.current()
                        async with old_pool.connection() as connection:
                            await connection.execute(
                                sql.SQL("INSERT INTO {} VALUES ('admitted-once')").format(sql.Identifier(table))
                            )
                            task, _incoming, outgoing, initial = await open_stream(app)
                            try:
                                await admin.execute(
                                    sql.SQL("ALTER ROLE {} PASSWORD {}").format(
                                        sql.Identifier(role), sql.Literal(password)
                                    )
                                )
                                server.keys = dict([server.second])
                                publish(
                                    "b",
                                    {
                                        **server.values((server.second[0], "private-invalid-secret")),
                                        "DATABASE_URL": second_uri,
                                    },
                                )
                                with pytest.raises(SecretReloadError):
                                    await app.state.coordinator.reload()
                                assert [ack["status"] for ack in server.acks] == ["applied", "failed"]
                                for binding, error in [
                                    (storage, ObjectStorageBindingError),
                                    (database, PostgresPoolBindingError),
                                ]:
                                    with pytest.raises(error):
                                        binding.acquire()
                                assert not old_pool.closed and created[0] not in retired
                                with pytest.raises(psycopg.OperationalError):
                                    await psycopg.AsyncConnection.connect(first_uri, connect_timeout=2)
                                with pytest.raises(ClientError):
                                    await created[0].client.head_object(Bucket="assets", Key="readiness.txt")
                                server.release.set()
                                assert (
                                    await finish_stream(task, outgoing, initial) == hashlib.sha256(server.data).digest()
                                )
                            finally:
                                server.release.set()
                                if not task.done():
                                    task.cancel()
                                await asyncio.gather(task, return_exceptions=True)
                    await asyncio.gather(database.wait_for_retirement(3), storage.wait_for_retirement(3))
                    assert old_pool.closed and created[0] in retired
                    cursor = await admin.execute(sql.SQL("SELECT count(*) FROM {}").format(sql.Identifier(table)))
                    assert (await cursor.fetchone())[0] == 1
                    publish("c", {**server.values(server.second), "DATABASE_URL": second_uri})
                    await app.state.coordinator.reload()
                    server.hold_head = True
                    server.head_entered.clear()
                    cancel = asyncio.Event()
                    pending = asyncio.create_task(app.state.coordinator.reload(cancel=cancel))
                    await asyncio.wait_for(server.head_entered.wait(), 3)
                    cancel.set()
                    with pytest.raises(SecretReloadError):
                        await pending
                    await asyncio.gather(database.wait_for_retirement(3), storage.wait_for_retirement(3))
                    assert [ack["status"] for ack in server.acks] == ["applied", "failed", "applied"]
                    for binding, error in [(storage, ObjectStorageBindingError), (database, PostgresPoolBindingError)]:
                        with pytest.raises(error):
                            binding.acquire()
                    server.hold_head = False
                    server.head_release.set()
                    publish("d", {**server.values(server.second), "DATABASE_URL": second_uri})
                    await app.state.coordinator.reload()
                    publish("e", {"DATABASE_URL": second_uri})
                    with pytest.raises(SecretReloadError):
                        await app.state.coordinator.reload()
                    assert server.acks[-1]["status"] == "failed"
                assert all(pool.closed for pool in pools)
                assert len(created) == len(retired)

    previous = signal.signal(signal.SIGHUP, signal.SIG_IGN)
    try:
        asyncio.run(scenario())
    finally:
        signal.signal(signal.SIGHUP, previous)
