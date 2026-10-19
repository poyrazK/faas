"""Application SQL/restore adapter over canonical, vendored SDK lifecycles."""

import asyncio
import logging
import os
from dataclasses import dataclass

from psycopg.conninfo import conninfo_to_dict
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool
from runtime.async_postgres_pool_runtime import AsyncPostgresPoolBinding
from runtime.async_secret_reload_runtime import (
    AsyncSecretConsumer,
    AsyncSecretReloadCancellation,
    AsyncSecretReloadCoordinator,
)
from runtime.postgres_pool_runtime import _configuration

logger = logging.getLogger(__name__)
READINESS_SQL = "SELECT id FROM public.notes LIMIT 1"


class DatabaseError(Exception):
    """Stable application error without driver/credential diagnostics."""


def connection_kwargs(uri):
    _configuration("DATABASE_URL", {"DATABASE_URL": uri})
    parsed = conninfo_to_dict(uri)
    if parsed.get("sslmode", "verify-full") not in {"require", "verify-ca", "verify-full"}:
        raise DatabaseError("database_tls_required")
    # Managed URLs may advertise require. Always verify the hostname and chain;
    # choose system trust explicitly rather than ambient PGSSLROOTCERT.
    return {"sslmode": "verify-full", "sslrootcert": parsed.get("sslrootcert") or "system",
            "connect_timeout": 3, "row_factory": dict_row}


@dataclass(eq=False)
class _Work:
    cancel: asyncio.Event
    task: asyncio.Task


def _consume(task):
    if not task.cancelled():
        task.exception()


class Database:
    def __init__(self, *, env=None, io=None, pool_factory=AsyncConnectionPool,
                 sql_timeout=3, restore_timeout=1.5, shutdown_timeout=5):
        settings = dict(os.environ if env is None else env)
        if "MIGRATION_DATABASE_URL" in settings:
            raise DatabaseError("migration_credentials_forbidden")
        self._sql_timeout = sql_timeout
        self._restore_timeout = min(restore_timeout, 1.5)
        self._shutdown_timeout = shutdown_timeout
        self._active = set()
        self._starting = self._restoring = self._closing = self._checkpointing = None
        self._restore_cancel = None
        self._started = self._closed = self._restore_failed = False
        self._checkpointed = self._drained = False
        self.generation = 0

        async def factory(config, cancel):
            cancel.check()
            kwargs = connection_kwargs(config.connection_string)
            kwargs["options"] = "-c statement_timeout=3000 -c idle_in_transaction_session_timeout=10000"
            pool = pool_factory(conninfo=config.connection_string, kwargs=kwargs, open=False,
                                min_size=0, max_size=5, max_waiting=16, timeout=3, reconnect_timeout=3)
            try:
                await pool.open()
                cancel.check()
                return pool
            except BaseException:
                await pool.close(timeout=3)
                raise

        async def validate(pool, cancel):
            async with asyncio.timeout(self._sql_timeout):
                await self._transaction(pool, READINESS_SQL, (), False, cancel)

        async def retire(pool):
            await pool.close(timeout=3)

        self.binding = AsyncPostgresPoolBinding("DATABASE_URL", factory, validate=validate, retire=retire,
                                                on_retire_error=lambda _error: logger.warning("database_pool_close_failed"))
        consumer = self.binding.secret_consumer()

        async def apply(snapshot, cancel):
            if "MIGRATION_DATABASE_URL" in snapshot.secrets:
                raise DatabaseError("migration_credentials_forbidden")
            # A pool built after the checkpoint drain would be captured; restore
            # rereads the latest snapshot anyway.
            if self._checkpointed and self._restoring is None:
                raise DatabaseError("database_unavailable")
            await consumer.apply(snapshot, cancel)

        self.coordinator = AsyncSecretReloadCoordinator(
            [AsyncSecretConsumer(apply, consumer.invalidate)], env=settings, io=io,
            on_error=lambda _error: logger.warning("database_adoption_failed"),
        )

    async def start(self):
        if self._closed:
            raise DatabaseError("database_unavailable")
        if self._starting is None:
            self._starting = asyncio.create_task(self.coordinator.start())
            self._starting.add_done_callback(_consume)
        try:
            await asyncio.shield(self._starting)
            if self._closed:
                raise DatabaseError("database_unavailable")
            self._started = True
        except asyncio.CancelledError:
            self.coordinator.stop()
            raise
        except Exception:
            raise DatabaseError("database_unavailable") from None

    async def _transaction(self, pool, text, parameters, write, cancel):
        cancel.check()
        async with pool.connection(timeout=3) as connection:
            cancel.check()
            async with connection.transaction():
                if not write:
                    await connection.execute("SET TRANSACTION READ ONLY")
                    cancel.check()
                await connection.execute("SELECT set_config('statement_timeout', %s, true)",
                                         (f"{max(1, int(self._sql_timeout * 1000))}ms",))
                cancel.check()
                cursor = await connection.execute(text, parameters)
                rows = await cursor.fetchall()
                cancel.check()  # Before COMMIT; never replay an uncertain outcome.
            cancel.check()
        return rows

    @staticmethod
    def _cancel(work):
        work.cancel.set()
        if not work.task.done() and not work.task.cancelling():
            work.task.cancel()

    async def query(self, text, parameters=(), *, write=False):
        if (not self._started or self._closed or self._restoring is not None or self._restore_failed
                or self._checkpointed):
            raise DatabaseError("database_unavailable")
        if len(self._active) >= 16:
            raise DatabaseError("database_busy")
        try:
            lease = self.binding.acquire()
        except Exception:
            raise DatabaseError("database_unavailable") from None
        cancel = asyncio.Event()
        task = asyncio.create_task(self._transaction(lease.current(), text, parameters, write,
                                                     AsyncSecretReloadCancellation([cancel])))
        work = _Work(cancel, task)
        self._active.add(work)

        def finished(done):
            # Even cancellation before the first task step releases its lease.
            # A driver that suppresses cancellation retains it until actual return.
            lease.release()
            self._active.discard(work)
            _consume(done)

        task.add_done_callback(finished)
        try:
            async with asyncio.timeout(self._sql_timeout):
                return await asyncio.shield(task)
        except asyncio.CancelledError:
            self._cancel(work)
            if asyncio.current_task().cancelling():
                raise
            raise DatabaseError("database_cancelled") from None
        except BaseException:
            self._cancel(work)
            raise DatabaseError("database_unavailable") from None

    async def reload(self):
        if not self._started or self._closed or self._checkpointed:
            raise DatabaseError("database_unavailable")
        await self.coordinator.reload(timeout=self._sql_timeout)

    async def checkpoint(self):
        """before_checkpoint (ADR-351): close admission, cancel unfinished work
        and wait until every session has closed, so a capture holds no
        connection or open transaction. A guest is then captured or destroyed,
        never resumed, so only restore() reopens the pool. A retried capture
        finds it drained; a call after a failed drain drains again."""
        if not self._started or self._closed:
            raise DatabaseError("database_unavailable")
        if self._restoring is not None and not self._restoring.done():
            raise DatabaseError("database_busy")
        if self._drained:
            return
        if self._checkpointing is None or self._checkpointing.done():
            self._checkpointed = True
            for work in tuple(self._active):
                self._cancel(work)
            self.binding.invalidate()
            self._checkpointing = asyncio.create_task(self._drain_for_checkpoint())
            self._checkpointing.add_done_callback(_consume)
        await asyncio.shield(self._checkpointing)

    async def _drain_for_checkpoint(self):
        try:
            await self.binding.wait_for_retirement(self._restore_timeout)
            if self._closed:
                raise DatabaseError("database_unavailable")
            self._drained = True
        except BaseException:
            logger.warning("database_checkpoint_drain_failed")
            raise DatabaseError("database_unavailable") from None

    async def restore(self):
        if not self._started or self._closed:
            raise DatabaseError("database_unavailable")
        if self._checkpointing is not None and not self._checkpointing.done():
            raise DatabaseError("database_busy")
        if self._restoring is not None and self._restoring.done():
            self._restoring = None
        owner = self._restoring is None
        if owner:
            self._restore_failed = True
            for work in tuple(self._active):
                self._cancel(work)
            self.binding.invalidate()
            self._restore_cancel = asyncio.Event()
            self._restoring = asyncio.create_task(self._recover(self._restore_cancel))
            self._restoring.add_done_callback(_consume)
            self._restoring.add_done_callback(self._restore_finished)
        task = self._restoring
        try:
            await asyncio.shield(task)
        except asyncio.CancelledError:
            if owner and not task.done():
                self._restore_cancel.set()
                self.binding.invalidate()
                task.cancel()
                # Drain the bounded coordinator attempt, not arbitrary driver
                # work. Its pool candidate stays owned by the SDK until return.
                await asyncio.gather(asyncio.shield(task), return_exceptions=True)
            raise
        finally:
            if task.done() and self._restoring is task:
                self._restoring = None

    def _restore_finished(self, task):
        if self._restoring is task:
            self._restoring = None

    async def _recover(self, cancel):
        try:
            async with asyncio.timeout(self._restore_timeout):
                await self.coordinator.reload(timeout=self._restore_timeout, cancel=cancel)
                await self.binding.wait_for_retirement()
                lease = self.binding.acquire()
                lease.release()
                if cancel.is_set() or self._closed:
                    raise DatabaseError("database_unavailable")
                self.generation += 1
                self._restore_failed = False
                self._checkpointed = self._drained = False
        except BaseException:
            self.binding.invalidate()
            logger.warning("database_restore_failed")
            raise DatabaseError("database_unavailable") from None

    async def close(self):
        if self._closing is None:
            self._closed = True
            self.coordinator.stop()
            if self._restore_cancel is not None:
                self._restore_cancel.set()
            for work in tuple(self._active):
                self._cancel(work)
            self._closing = asyncio.create_task(self._drain())
            self._closing.add_done_callback(_consume)
        await asyncio.shield(self._closing)

    async def _drain(self):
        try:
            async with asyncio.timeout(self._shutdown_timeout):
                await self.coordinator.wait_stopped()
                if self._restoring is not None:
                    await asyncio.gather(asyncio.shield(self._restoring), return_exceptions=True)
                await self.binding.wait_for_retirement()
        except BaseException:
            raise DatabaseError("database_cleanup_incomplete") from None
