"""Run one process with: uvicorn app:app --host 0.0.0.0 --port 8080."""

import asyncio
import logging
from contextlib import asynccontextmanager

import psycopg
from database import make_binding
from faas_sdk.async_secret_reload_runtime import AsyncSecretReloadCoordinator
from faas_sdk.postgres_pool_runtime import PostgresPoolBindingError

logger = logging.getLogger(__name__)
from fastapi import FastAPI, HTTPException
from psycopg_pool import PoolClosed, PoolTimeout, TooManyRequests


def create_app(*, env=None, io=None) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app):
        database = make_binding()
        coordinator = AsyncSecretReloadCoordinator(
            [database.secret_consumer()], env=env, io=io,
            on_error=lambda _error: logger.warning("database_adoption_failed"),
        )
        app.state.database, app.state.coordinator = database, coordinator
        try:
            await coordinator.start()
            yield
        finally:
            coordinator.stop()
            try:
                async with asyncio.timeout(5):
                    await coordinator.wait_stopped()
                    await database.wait_for_retirement()
            except (TimeoutError, PostgresPoolBindingError):
                logger.warning("database_cleanup_incomplete")

    application = FastAPI(lifespan=lifespan)

    @application.get("/healthz")
    async def health():
        return {"status": "alive"}

    @application.get("/db-time")
    async def database_time():
        try:
            async with asyncio.timeout(3):
                # Connection context commits/rolls back and returns the connection
                # BEFORE the outer pool lease releases, including cancellation.
                async with (
                    application.state.database.acquire() as lease,
                    lease.current().connection(timeout=3) as connection,
                ):
                    cursor = await connection.execute("SELECT CURRENT_TIMESTAMP")
                    row = await cursor.fetchone()
            return {"time": row[0].isoformat()}
        except (TimeoutError, PostgresPoolBindingError, psycopg.Error, PoolClosed, PoolTimeout, TooManyRequests):
            raise HTTPException(status_code=503, detail="Database temporarily unavailable") from None

    return application


app = create_app()
