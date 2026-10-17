"""Immediate callback worker for authenticated, application-owned deferred event jobs."""

from __future__ import annotations

import asyncio
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from typing import Any

from ._event_acknowledgment_callbacks import call
from ._event_acknowledgment_protocol import (
    CallbackFailure,
    Control,
    EventAcknowledgmentWorkerCancellation,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerError,
    EventAcknowledgmentWorkerOptions,
    EventAcknowledgmentWorkerResult,
    callback_error,
    observation,
    pause,
    reason_code,
    renewed,
    settings,
)
from .client import AuthenticatedClient
from .event_consumer_delivery_context import event_consumer_delivery_context_from_headers
from .event_consumer_transactions import (
    EventConsumerCommitUnknownError,
    EventConsumerTransactionResult,
    _awith_event_consumer_transaction,
    event_consumer_request_from_headers,
)

_SUPPORTED = object()
Handler = Callable[[Any, EventAcknowledgmentWorkerCancellation], Awaitable[bytes | str]]


def _header(headers: Mapping[str, str | Sequence[str]], name: str) -> str:
    values = []
    for key, value in headers.items():
        if not isinstance(key, str):
            raise ValueError("invalid acknowledgment headers")
        if key.lower() == name:
            if isinstance(value, str):
                values.append(value)
            elif isinstance(value, Sequence):
                values.extend(value)
            else:
                raise ValueError("invalid acknowledgment header")
    if len(values) != 1 or not isinstance(values[0], str) or not values[0]:
        raise ValueError("missing or duplicate acknowledgment header")
    return values[0]


class AsyncEventAcknowledgmentWorker:
    """Factory-created capability. Own one loop; keep durable jobs exclusively owned."""

    __slots__ = ("_client", "_path", "_token", "_input", "_delivery", "_budget", "_busy", "_loop")

    def __init__(self, key: object):
        if key is not _SUPPORTED:
            raise TypeError("use AsyncEventAcknowledgmentWorker.from_headers")

    @classmethod
    def from_headers(
        cls,
        client: AuthenticatedClient,
        app: str,
        headers: Mapping[str, str | Sequence[str]],
        body: bytes,
        account_id: str,
        app_id: str,
        options: EventAcknowledgmentWorkerOptions | None = None,
    ) -> AsyncEventAcknowledgmentWorker:
        """Authenticate ingress first; persist original bytes/headers securely before returning 202."""
        if (
            not isinstance(client, AuthenticatedClient)
            or not isinstance(app, str)
            or re.fullmatch(r"[a-z0-9][a-z0-9-]{0,252}", app) is None
            or not isinstance(headers, Mapping)
            or not isinstance(body, bytes)
        ):
            raise ValueError("invalid event acknowledgment delivery")
        # Snapshot sequences; retain only validated request/context and the opaque token.
        snapshot = {
            key: tuple(value) if isinstance(value, Sequence) and not isinstance(value, str) else value
            for key, value in headers.items()
        }
        token = _header(snapshot, "x-gregale-event-acknowledgment-token")
        lease = _header(snapshot, "x-gregale-event-acknowledgment-lease-seconds")
        if (
            re.fullmatch(r"[a-f0-9]{64}", token) is None
            or re.fullmatch(r"[1-9][0-9]{0,2}", lease) is None
            or int(lease) > 300
        ):
            raise ValueError("invalid acknowledgment token or lease")
        worker = cls(_SUPPORTED)
        worker._input = event_consumer_request_from_headers(snapshot, body, account_id, app_id)
        worker._delivery = event_consumer_delivery_context_from_headers(snapshot, body, account_id, app_id)
        worker._budget = settings(options, int(lease))
        worker._client = client.get_async_httpx_client()
        worker._path = f"/v1/apps/{app}/event-deliveries/{worker._delivery.invocation_id}/acknowledgment"
        worker._token, worker._busy, worker._loop = token, False, None
        return worker

    def __repr__(self) -> str:
        return "AsyncEventAcknowledgmentWorker{redacted}"

    __str__ = __repr__

    def __reduce_ex__(self, _protocol: int) -> Any:
        raise TypeError("persist the protected original delivery, not a worker capability")

    def __copy__(self) -> Any:
        raise TypeError("worker capabilities cannot be copied")

    def __deepcopy__(self, _memo: Any) -> Any:
        raise TypeError("worker capabilities cannot be copied")

    async def run(
        self, connection: Any, handler: Handler, *, stop_event: asyncio.Event | None = None
    ) -> EventAcknowledgmentWorkerResult:
        """Commit business SQL/receipt, join renewal, then send exact saved completion JSON."""
        if not callable(handler):
            raise ValueError("invalid event consumer handler")

        async def process(control: Control) -> EventAcknowledgmentWorkerResult:
            try:
                receipt = await self._receipt(connection, control)
            except EventAcknowledgmentWorkerError as error:
                if error.code != "event_consumer_receipt_missing":
                    raise
                await self._start(control)
                monitor = asyncio.create_task(self._monitor(control))
                try:
                    receipt = await self._transaction(
                        connection,
                        lambda cursor: handler(cursor, EventAcknowledgmentWorkerCancellation(control)),
                        control,
                    )
                    control.result = EventAcknowledgmentWorkerResult(True, receipt)
                except EventConsumerCommitUnknownError:
                    raise
                except BaseException:
                    control.check()
                    raise
                finally:
                    monitor.cancel()
                    await asyncio.gather(monitor, return_exceptions=True)
            control.result = EventAcknowledgmentWorkerResult(True, receipt)
            return await self._terminal("complete", receipt.body, control)

        return await self._operate(process, stop_event)

    async def complete_receipt(
        self, connection: Any, *, stop_event: asyncio.Event | None = None
    ) -> EventAcknowledgmentWorkerResult:
        """Receipt-only recovery after unknown COMMIT/callback. Never invokes business code."""

        async def complete(control: Control) -> EventAcknowledgmentWorkerResult:
            receipt = await self._receipt(connection, control)
            control.result = EventAcknowledgmentWorkerResult(True, receipt)
            return await self._terminal("complete", receipt.body, control)

        return await self._operate(complete, stop_event)

    async def retry(
        self, connection: Any, reason: str, *, stop_event: asyncio.Event | None = None
    ) -> EventAcknowledgmentWorkerResult:
        """Persist this decision first; requires stopped work, known rollback and exclusive job ownership."""
        return await self._reject(connection, "retry", reason, stop_event)

    async def dead_letter(
        self, connection: Any, reason: str, *, stop_event: asyncio.Event | None = None
    ) -> EventAcknowledgmentWorkerResult:
        """Same ownership/known-rollback requirements as retry; receipt presence blocks rejection."""
        return await self._reject(connection, "dead-letter", reason, stop_event)

    async def _reject(
        self, connection: Any, action: str, reason: str, stop_event: asyncio.Event | None
    ) -> EventAcknowledgmentWorkerResult:
        if not reason_code(reason):
            raise ValueError("invalid event acknowledgment failure reason")

        async def reject(control: Control) -> EventAcknowledgmentWorkerResult:
            try:
                receipt = await self._receipt(connection, control)
            except EventAcknowledgmentWorkerError as error:
                if error.code != "event_consumer_receipt_missing":
                    raise
            else:
                raise EventAcknowledgmentWorkerError(
                    "event_consumer_receipt_committed", EventAcknowledgmentWorkerResult(True, receipt)
                )
            return await self._terminal(action, reason, control)

        return await self._operate(reject, stop_event)

    async def _receipt(self, connection: Any, control: Control) -> EventConsumerTransactionResult:
        async def missing(_cursor: Any) -> bytes:
            raise EventAcknowledgmentWorkerError("event_consumer_receipt_missing")

        return await self._transaction(connection, missing, control)

    async def _transaction(
        self, connection: Any, handler: Callable[[Any], Awaitable[bytes | str]], control: Control
    ) -> EventConsumerTransactionResult:
        try:
            return await _awith_event_consumer_transaction(connection, self._input, handler, control.check)
        except EventConsumerCommitUnknownError:
            # Discard this exclusively leased connection before owner reconciliation.
            # The owned task remains shielded while SQL/close cleanup is joined.
            try:
                await connection.close()
            except BaseException:
                pass  # A close failure cannot turn an unknown COMMIT into known rollback.
            raise

    async def _start(self, control: Control) -> None:
        for attempt in range(self._budget.attempts):
            try:
                raw = await call(self._client, self._path, self._token, "renew", self._budget, control)
            except CallbackFailure as error:
                delay, offered = error.delay_ms, False
                if error.status == 409:
                    try:
                        current = observation(
                            self._delivery,
                            await call(self._client, self._path, self._token, "get", self._budget, control),
                        )
                        if current.state != "dispatching" or current.processing_state != "delivering":
                            raise CallbackFailure(lost=True)
                    except CallbackFailure as read_error:
                        raise callback_error(read_error, control.result) from None
                    delay, offered = self._budget.backoff_ms, True
                if delay is None or attempt + 1 == self._budget.attempts:
                    if offered:
                        raise EventAcknowledgmentWorkerError(
                            "event_acknowledgment_unconfirmed", control.result, 409
                        ) from None
                    raise callback_error(error, control.result) from None
                await pause(delay, control)
                continue
            try:
                renewed(observation(self._delivery, raw))
            except CallbackFailure as error:
                raise callback_error(error, control.result) from None
            control.check()
            return

    async def _monitor(self, control: Control) -> None:
        try:
            while True:
                await pause(self._budget.interval_ms, control)
                renewed(
                    observation(
                        self._delivery,
                        await call(self._client, self._path, self._token, "renew", self._budget, control),
                    )
                )
        except CallbackFailure as error:
            control.stop(callback_error(error, control.result))
        except (asyncio.CancelledError, EventAcknowledgmentWorkerError):
            pass
        except Exception:
            control.stop(EventAcknowledgmentWorkerError("event_acknowledgment_unconfirmed"))

    async def _terminal(self, action: str, value: bytes | str, control: Control) -> EventAcknowledgmentWorkerResult:
        for attempt in range(self._budget.attempts):
            try:
                status = observation(
                    self._delivery,
                    await call(self._client, self._path, self._token, action, self._budget, control, value),
                )
                valid = (
                    (
                        status.state == "completed"
                        and status.processing_state == "completed"
                        and not status.disposition
                        and not status.failure_reason
                    )
                    if action == "complete"
                    else (
                        status.disposition == action
                        and status.failure_reason == value
                        and status.lease_expires_at is None
                        and (
                            action == "retry"
                            and status.state == "pending"
                            and status.processing_state == "waiting"
                            and status.next_attempt_at is not None
                            or status.state == "dead_letter"
                            and status.processing_state == "dead_letter"
                            and status.next_attempt_at is None
                        )
                    )
                )
                if not valid:
                    raise CallbackFailure()
                return EventAcknowledgmentWorkerResult(control.result.receipt_confirmed, control.result.receipt, status)
            except CallbackFailure as error:
                if error.delay_ms is None or attempt + 1 == self._budget.attempts:
                    raise callback_error(error, control.result) from None
                await pause(error.delay_ms, control)
        raise EventAcknowledgmentWorkerError("event_acknowledgment_unconfirmed", control.result)

    async def _operate(
        self,
        operation: Callable[[Control], Awaitable[EventAcknowledgmentWorkerResult]],
        stop_event: asyncio.Event | None,
    ) -> EventAcknowledgmentWorkerResult:
        if stop_event is not None and not isinstance(stop_event, asyncio.Event):
            raise ValueError("invalid event acknowledgment stop event")
        if stop_event is not None and stop_event.is_set():
            raise EventAcknowledgmentWorkerCancelled()
        current = asyncio.current_task()
        if current is not None and current.cancelling():
            raise EventAcknowledgmentWorkerCancelled()
        loop = asyncio.get_running_loop()
        if self._loop is not None and self._loop is not loop:
            raise ValueError("worker belongs to another event loop")
        if self._busy:
            raise EventAcknowledgmentWorkerError("event_acknowledgment_worker_busy")
        self._loop, self._busy = loop, True
        control = Control(stop_event, current)

        async def forward() -> None:
            try:
                await stop_event.wait()
            except Exception:
                control.stop(EventAcknowledgmentWorkerError("event_acknowledgment_unconfirmed"))
            else:
                control.stop()

        async def owned() -> EventAcknowledgmentWorkerResult:
            watcher = asyncio.create_task(forward()) if stop_event is not None else None
            try:
                if stop_event is not None and stop_event.is_set():
                    control.stop()
                return await operation(control)
            finally:
                if watcher is not None:
                    watcher.cancel()
                    await asyncio.gather(watcher, return_exceptions=True)

        pending = asyncio.create_task(owned())
        try:
            while True:
                try:
                    return await asyncio.shield(pending)
                except asyncio.CancelledError:
                    if pending.done():
                        try:
                            return pending.result()
                        except asyncio.CancelledError:
                            raise EventAcknowledgmentWorkerCancelled(control.result) from None
                    control.stop()
        finally:
            self._busy = False
