"""Application-owned psycopg hooks; SDK core has no driver dependency."""

from faas_sdk.preview.async_postgres_pool_runtime import AsyncPostgresPoolBinding
from psycopg.conninfo import conninfo_to_dict
from psycopg_pool import AsyncConnectionPool


def make_binding() -> AsyncPostgresPoolBinding[AsyncConnectionPool]:
    async def factory(config, cancel):
        cancel.check()
        # Application TLS policy: preserve the URI's CA options and verify both
        # the certificate chain and hostname. No fallback to ambient PGSSLMODE.
        if conninfo_to_dict(config.connection_string).get("sslmode") != "verify-full":
            raise ValueError("Database requires verified TLS")
        pool = AsyncConnectionPool(
            conninfo=config.connection_string, open=False, min_size=0, max_size=5,
            timeout=3, max_waiting=16, reconnect_timeout=3,
            kwargs={"connect_timeout": 3,
                    "options": "-c statement_timeout=3000 -c idle_in_transaction_session_timeout=10000"},
        )
        try:
            await pool.open()
            cancel.check()
            return pool
        except BaseException:
            # The factory owns allocations until it returns the pool.
            await pool.close(timeout=3)
            raise

    async def validate(pool, cancel):
        cancel.check()
        async with pool.connection(timeout=3) as connection:
            cursor = await connection.execute("SELECT 1")
            await cursor.fetchone()
        cancel.check()

    async def retire(pool):
        await pool.close(timeout=3)

    return AsyncPostgresPoolBinding("DATABASE_URL", factory, validate=validate, retire=retire)
