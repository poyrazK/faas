"""Owned PostgreSQL transactions; staging never retries a business callback."""

from __future__ import annotations

import math
import re
from collections.abc import Awaitable, Callable, Sequence
from typing import Any, Protocol, TypeVar

from ..job_runtime import JobError, _scope
from .event_outbox_state import EventOutboxError

T = TypeVar("T")


class EventOutboxConnection(Protocol):
    async def query(self, sql: str, values: list[Any]) -> list[dict[str, Any]]: ...
    async def execute(self, sql: str, values: Sequence[Any]) -> Any: ...
    async def release(self, discard: bool = False) -> None: ...


class EventOutboxPool(Protocol):
    async def connect(self) -> EventOutboxConnection: ...


class _PsycopgConnection:
    def __init__(self, connection: Any):
        self.connection = connection

    async def execute(self, sql: str, values: Sequence[Any]) -> Any:
        return await self.connection.execute(sql, values)

    async def query(self, sql: str, values: list[Any]) -> list[dict[str, Any]]:
        # Only SDK-owned statements use $n. Business execute() retains psycopg's
        # native parameter style, with no rewriting of literals or comments.
        ordered = [values[int(m[1]) - 1] for m in re.finditer(r"\$(\d+)", sql)]
        cursor = await self.connection.execute(re.sub(r"\$\d+", "%s", sql), ordered)
        if cursor.description is None:
            return []
        rows = await cursor.fetchall()
        names = [column.name for column in cursor.description]
        return [dict(row) if isinstance(row, dict) else dict(zip(names, row, strict=True)) for row in rows]

    async def release(self, discard: bool = False) -> None:
        await self.connection.close()


class PsycopgEventOutboxPool:
    """Factory must return a fresh, idle connection owned by this adapter.

    No psycopg dependency is imported by the SDK. The adapter configures owned
    connections for explicit transactions and closes each after use.
    """

    def __init__(self, connect: Callable[[], Awaitable[Any]]):
        self._connect = connect

    async def connect(self) -> EventOutboxConnection:
        connection = await self._connect()
        if connection.info.transaction_status != 0:
            await connection.close()
            raise EventOutboxError("invalid_transaction")
        try:
            if not connection.autocommit:
                await connection.set_autocommit(True)
            await connection.execute("SET client_encoding='UTF8'")
            return _PsycopgConnection(connection)
        except BaseException:
            await connection.close()
            raise


_SUPPORTED = object()


class EventOutboxTransaction:
    def __init__(self, connection: EventOutboxConnection, check: Callable[[], None], support: object):
        self._connection, self._check, self._support = connection, check, support
        self._active = support is _SUPPORTED
        self._failure: EventOutboxError | None = None

    def _valid(self) -> None:
        if not self._active or self._support is not _SUPPORTED:
            raise EventOutboxError("invalid_transaction")
        self._check()

    async def _query(self, sql: str, values: list[Any]) -> list[dict[str, Any]]:
        self._valid()
        try:
            result = await self._connection.query(sql, values)
            self._valid()
            return result
        except Exception:
            self._check()
            self._failure = self._failure or EventOutboxError("store_failed")
            raise self._failure from None

    async def execute(self, sql: str, values: Sequence[Any] = ()) -> Any:
        """Business SQL uses the selected driver's native parameter style."""
        self._valid()
        try:
            result = await self._connection.execute(sql, values)
            self._valid()
            return result
        except Exception:
            self._check()
            self._failure = self._failure or EventOutboxError("store_failed")
            raise self._failure from None

    def _fail(self, error: EventOutboxError) -> None:
        self._failure = self._failure or error


async def with_event_outbox_transaction(
    pool: EventOutboxPool, handler: Callable[[EventOutboxTransaction], Awaitable[T]], *, timeout: float
) -> T:
    if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 2147483.647:
        raise EventOutboxError("invalid_request")
    milliseconds = math.ceil(timeout * 1000)
    connection: EventOutboxConnection | None = None
    transaction: EventOutboxTransaction | None = None
    commit_sent = committed = released = False

    async def release(discard: bool) -> None:
        nonlocal released
        if connection is not None and not released:
            released = True
            await connection.release(discard)

    async def run(check: Callable[[], None]) -> T:
        nonlocal connection, transaction, commit_sent, committed
        try:
            connection = await pool.connect()
            check()
            await connection.query("BEGIN ISOLATION LEVEL READ COMMITTED", [])
            check()
            await connection.query(
                "SELECT set_config('statement_timeout',$1,true),set_config('idle_in_transaction_session_timeout',$1,true)",
                [str(milliseconds)],
            )
            check()
            transaction = EventOutboxTransaction(connection, check, _SUPPORTED)
            value = await handler(transaction)
            check()
            transaction._active = False
            if transaction._failure is not None:
                raise transaction._failure
            commit_sent = True
            try:
                await connection.query("COMMIT", [])
                committed = True
            except Exception:
                raise EventOutboxError("commit_unknown") from None
            return value
        finally:
            if transaction is not None:
                transaction._active = False
            if connection is not None and not released:
                if not committed:
                    try:
                        await connection.query("ROLLBACK", [])
                    except Exception:
                        await release(True)
                await release(not committed)

    try:
        return await _scope(timeout, run)
    except BaseException as error:
        if transaction is not None:
            transaction._active = False
        await release(True)
        if commit_sent and not committed:
            raise EventOutboxError("commit_unknown") from None
        if isinstance(error, EventOutboxError):
            raise
        if isinstance(error, JobError) and error.code == "deadline":
            raise EventOutboxError("deadline") from None
        if not isinstance(error, Exception):
            raise
        raise EventOutboxError("store_failed") from None
