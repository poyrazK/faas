"""Bounded live-only direct-message handling and processing receipt confirmation."""

from __future__ import annotations

import asyncio
import base64
import re
from collections import deque
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any

from .realtime_connection_status import RealtimeConnectionStatusTracker
from .realtime_recovery import RealtimeRecoveryError, RealtimeRecoveryRetry


def _message_id(value: Any) -> None:
    if (
        not isinstance(value, str)
        or not 1 <= len(value) <= 128
        or any(not "!" <= c <= "~" or c in "/?#" for c in value)
    ):
        raise RealtimeRecoveryError("invalid_direct_message_id")


@dataclass(frozen=True, kw_only=True)
class RealtimeDirectMessage:
    message_id: str | None
    data: bytes = field(repr=False)
    binary: bool
    receipt_requested: bool


class RealtimeWebSocketDirectMessages:
    """One worker per physical socket; reconnect does not replay queued messages.

    The async handler must complete effects before returning. Receipt retries
    resend only the ACK, never call the application handler again. No read,
    channel, filter, inbox or durable checkpoint is advanced by this helper.
    """

    def __init__(
        self,
        *,
        on_message: Callable[[RealtimeDirectMessage], Awaitable[None]],
        on_error: Callable[[str], None] | None = None,
        buffer_size: int = 128,
        acknowledgement_timeout_seconds: float = 5,
        acknowledgement_retries: int = 2,
    ):
        if not callable(on_message) or on_error is not None and not callable(on_error):
            raise ValueError("Direct-message callbacks must be callable")
        if type(buffer_size) is not int or not 1 <= buffer_size <= 8192:
            raise ValueError("Direct-message buffer must be between 1 and 8192")
        if (
            type(acknowledgement_timeout_seconds) not in (int, float)
            or not 0.1 <= acknowledgement_timeout_seconds <= 60
        ):
            raise ValueError("Receipt timeout must be between 0.1 and 60 seconds")
        if type(acknowledgement_retries) is not int or not 0 <= acknowledgement_retries <= 3:
            raise ValueError("Receipt retries must be between 0 and 3")
        self._handler, self._on_error = on_message, on_error
        self._size, self._timeout, self._retries = buffer_size, acknowledgement_timeout_seconds, acknowledgement_retries
        self._owner: Any = None
        self._send: Callable[[dict], Awaitable[None]] | None = None
        self._failure: Callable[[Exception], None] | None = None
        self._queue: asyncio.Queue[RealtimeDirectMessage] = asyncio.Queue(maxsize=buffer_size)
        self._worker: asyncio.Task[None] | None = None
        self._tasks: dict[asyncio.Task[None], Any] = {}
        self._pending: asyncio.Future[bool] | None = None
        self._pending_id: str | None = None
        self._recent: deque[str] = deque(maxlen=256)
        self._disposed = False
        self._error: str | None = None

    @property
    def error(self) -> str | None:
        return self._error

    def _bind(self, owner: Any, send: Callable, failure: Callable) -> None:
        if self._disposed or self._owner is not None and self._owner is not owner:
            raise RealtimeRecoveryError("direct_messages_already_bound_or_disposed")
        if self._owner is owner:
            return
        self._owner, self._send, self._failure = owner, send, failure
        self._error = None
        self._queue = asyncio.Queue(maxsize=self._size)
        self._recent.clear()
        task = asyncio.create_task(self._run(owner))
        self._worker = task
        self._tasks[task] = owner
        task.add_done_callback(lambda completed: self._tasks.pop(completed, None))

    def _report(self, reason: str) -> None:
        self._error = reason
        if self._on_error is not None:
            RealtimeConnectionStatusTracker._callback(self._on_error, reason)

    def _invalidate(self, owner: Any) -> None:
        if self._owner is not owner:
            return
        self._owner = self._send = self._failure = None
        self._recent.clear()
        if self._worker is not None and self._worker is not asyncio.current_task():
            self._worker.cancel()
        if self._pending is not None and not self._pending.done():
            self._pending.cancel()
        while not self._queue.empty():
            self._queue.get_nowait()

    async def _join(self, owner: Any = None) -> None:
        tasks = tuple(
            task
            for task, task_owner in self._tasks.items()
            if task is not asyncio.current_task() and (owner is None or task_owner is owner)
        )
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)

    async def aclose(self) -> None:
        """Dispose the handler; stop/close the owning client separately."""
        self._disposed = True
        self._invalidate(self._owner)
        await self._join()

    def _receive(self, owner: Any, frame: dict) -> None:
        if self._owner is not owner or self._disposed:
            raise RealtimeRecoveryError("unexpected_direct_message")
        kind = frame.get("type")
        if kind == "direct_message":
            identity = frame.get("message_id")
            requested, binary = frame.get("receipt_requested", False), frame.get("binary", False)
            encoded = frame.get("data_base64", "")
            if identity is not None:
                _message_id(identity)
            if (
                type(requested) is not bool
                or type(binary) is not bool
                or requested
                and identity is None
                or not isinstance(encoded, str)
                or re.fullmatch(r"(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?", encoded) is None
            ):
                raise RealtimeRecoveryError("invalid_direct_message")
            try:
                payload = base64.b64decode(encoded, validate=True)
            except (ValueError, TypeError):
                raise RealtimeRecoveryError("invalid_direct_message") from None
            if len(payload) > 4096:
                raise RealtimeRecoveryError("invalid_direct_message")
            try:
                self._queue.put_nowait(
                    RealtimeDirectMessage(
                        message_id=identity,
                        data=payload,
                        binary=binary,
                        receipt_requested=requested,
                    )
                )
            except asyncio.QueueFull:
                self._report("direct_message_overflow")
                raise RealtimeRecoveryRetry() from None
        elif kind in ("direct_message_acknowledged", "direct_message_ack_failed"):
            identity = frame.get("message_id")
            _message_id(identity)
            if identity != self._pending_id:
                if identity in self._recent:
                    return
                raise RealtimeRecoveryError("unexpected_direct_receipt")
            if self._pending is not None and not self._pending.done():
                self._pending.set_result(kind == "direct_message_acknowledged")
        else:
            raise RealtimeRecoveryError("unexpected_direct_message")

    async def _ack(self, owner: Any, identity: str) -> None:
        self._pending_id = identity
        try:
            for _ in range(self._retries + 1):
                if self._owner is not owner:
                    return
                self._pending = asyncio.get_running_loop().create_future()
                try:
                    async with asyncio.timeout(self._timeout):
                        await self._send({"type": "direct_message_ack", "message_id": identity})
                        confirmed = await asyncio.shield(self._pending)
                except TimeoutError:
                    confirmed = False
                if not self._pending.done():
                    self._pending.cancel()
                if confirmed:
                    self._recent.append(identity)
                    return
            self._recent.append(identity)
            self._report("direct_receipt_failed")
        finally:
            if self._pending is not None and not self._pending.done():
                self._pending.cancel()
            self._pending = None
            self._pending_id = None

    async def _run(self, owner: Any) -> None:
        try:
            while self._owner is owner:
                message = await self._queue.get()
                try:
                    await self._handler(message)
                except asyncio.CancelledError:
                    if asyncio.current_task().cancelling():
                        raise
                    raise RealtimeRecoveryError("direct_handler_failed") from None
                except Exception:
                    raise RealtimeRecoveryError("direct_handler_failed") from None
                if message.receipt_requested and self._owner is owner:
                    await self._ack(owner, message.message_id)
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            reason = "direct_handler_failed" if isinstance(exc, RealtimeRecoveryError) else "direct_transport_failed"
            self._report(reason)
            if self._owner is owner and self._failure is not None:
                self._failure(
                    RealtimeRecoveryError(reason) if reason == "direct_handler_failed" else RealtimeRecoveryRetry()
                )
