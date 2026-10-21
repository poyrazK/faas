"""Immediate and durable callback modes for authenticated, application-owned event jobs."""

from __future__ import annotations

import asyncio
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from typing import Any, TypeVar

from ._event_acknowledgment_callbacks import call, terminal
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
    owned_operation,
    pause,
    reason_code,
    renewed,
    settings,
)
from .client import AuthenticatedClient
from .event_acknowledgment_intent_retry_policy import (
    EventAcknowledgmentIntentRetryBudget,
    EventAcknowledgmentIntentRetryPolicy,
    EventAcknowledgmentIntentStoredRetryPolicy,
    _staging_policy,
)
from .event_acknowledgment_intents import (
    EventAcknowledgmentIntentResult,
    EventAcknowledgmentIntentStatus,
    _save_completion,
    _save_negative,
)
from .event_acknowledgment_preparation import (
    ConnectionFactory,
    EventAcknowledgmentPreparedOptions,
    _run_prepared,
    _settings,
)
from .event_acknowledgment_preparation_events import observe_preparation
from .event_consumer_delivery_context import event_consumer_delivery_context_from_headers
from .event_consumer_processing import EventConsumerProcessingLease
from .event_consumer_transactions import (
    EventConsumerCommitUnknownError,
    EventConsumerTransactionResult,
    _awith_event_consumer_transaction,
    _EventConsumerTransactionHooks,
    event_consumer_request_from_headers,
)

_SUPPORTED = object()
_T = TypeVar("_T")
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

    __slots__ = ("_client", "_path", "_token", "_input", "_delivery", "_budget", "_busy", "_loop", "_slug", "_seconds")

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
        worker._slug, worker._seconds = app, int(lease)
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
            receipt = await self._process(connection, handler, control)
            control.result = EventAcknowledgmentWorkerResult(True, receipt)
            return await self._terminal("complete", receipt.body, control)

        return await self._operate(process, stop_event)

    async def _process(
        self, connection: Any, handler: Handler, control: Control, hooks: _EventConsumerTransactionHooks | None = None
    ) -> EventConsumerTransactionResult:
        try:
            return await self._receipt(connection, control, hooks)
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
                hooks=hooks,
            )
            control.result = EventAcknowledgmentWorkerResult(True, receipt)
            return receipt
        except EventConsumerCommitUnknownError:
            raise
        except BaseException:
            control.check()
            raise
        finally:
            monitor.cancel()
            await asyncio.gather(monitor, return_exceptions=True)

    async def run_with_processing_lease(
        self,
        connection: Any,
        lease: EventConsumerProcessingLease,
        handler: Handler,
        *,
        stop_event: asyncio.Event | None = None,
    ) -> EventAcknowledgmentWorkerResult:
        """Stop/join local renewal first. Fence new writes and send exact committed completion."""
        if not isinstance(lease, EventConsumerProcessingLease) or not callable(handler):
            raise ValueError("invalid processing lease or event consumer handler")

        async def process(control: Control) -> EventAcknowledgmentWorkerResult:
            async with lease._use(self._input, control) as hooks:
                receipt = await self._process(connection, handler, control, hooks)
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

    async def run_durable(
        self,
        connection: Any,
        handler: Handler,
        *,
        stop_event: asyncio.Event | None = None,
        retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None,
        retry_budget: EventAcknowledgmentIntentRetryBudget | None = None,
    ) -> EventAcknowledgmentIntentResult:
        """Atomically commit business SQL, receipt and saved completion. Sends no terminal callback."""
        if not callable(handler):
            raise ValueError("invalid event consumer handler")

        policy = _staging_policy(retry_policy, retry_budget)
        return await self._operate(
            lambda control: self._durable(connection, handler, control, policy=policy), stop_event
        )

    async def _durable(
        self,
        connection: Any,
        handler: Handler,
        control: Control,
        hooks: _EventConsumerTransactionHooks | None = None,
        policy: EventAcknowledgmentIntentStoredRetryPolicy | None = None,
    ) -> EventAcknowledgmentIntentResult:
        try:
            return await self._stage(connection, None, control, hooks, policy)
        except EventAcknowledgmentWorkerError as error:
            if error.code != "event_consumer_receipt_missing":
                raise
        await self._start(control)
        monitor = asyncio.create_task(self._monitor(control))
        try:
            return await self._stage(
                connection,
                lambda cursor: handler(cursor, EventAcknowledgmentWorkerCancellation(control)),
                control,
                hooks,
                policy,
            )
        except EventConsumerCommitUnknownError:
            raise
        except BaseException:
            control.check()
            raise
        finally:
            monitor.cancel()
            await asyncio.gather(monitor, return_exceptions=True)

    async def run_durable_with_processing_lease(
        self,
        connection: Any,
        lease: EventConsumerProcessingLease,
        handler: Handler,
        *,
        stop_event: asyncio.Event | None = None,
        retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None,
        retry_budget: EventAcknowledgmentIntentRetryBudget | None = None,
    ) -> EventAcknowledgmentIntentResult:
        """Commit writes, receipt, unexpired claim removal and completion intent together."""
        if not isinstance(lease, EventConsumerProcessingLease) or not callable(handler):
            raise ValueError("invalid processing lease or event consumer handler")

        policy = _staging_policy(retry_policy, retry_budget)

        async def process(control: Control) -> EventAcknowledgmentIntentResult:
            async with lease._use(self._input, control) as hooks:
                return await self._durable(connection, handler, control, hooks, policy)

        return await self._operate(process, stop_event)

    async def save_completion_intent(
        self,
        connection: Any,
        *,
        stop_event: asyncio.Event | None = None,
        retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None,
        retry_budget: EventAcknowledgmentIntentRetryBudget | None = None,
    ) -> EventAcknowledgmentIntentResult:
        """Stage a committed receipt after unknown COMMIT. Never invokes business code."""
        policy = _staging_policy(retry_policy, retry_budget)
        return await self._operate(lambda control: self._stage(connection, None, control, policy=policy), stop_event)

    async def run_prepared_durable(
        self,
        connection_factory: ConnectionFactory,
        prepare: Callable[[EventAcknowledgmentWorkerCancellation], Awaitable[_T]],
        handler: Callable[[Any, EventAcknowledgmentWorkerCancellation, _T], Awaitable[bytes | str]],
        options: EventAcknowledgmentPreparedOptions,
        *,
        stop_event: asyncio.Event | None = None,
        retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None,
        retry_budget: EventAcknowledgmentIntentRetryBudget | None = None,
    ) -> EventAcknowledgmentIntentResult:
        """Own both lease lifetimes during preparation, then atomically stage fenced completion."""
        settings = _settings(options)
        policy = _staging_policy(retry_policy, retry_budget)
        if not callable(connection_factory) or not callable(prepare) or not callable(handler):
            raise ValueError("invalid acknowledgment preparation factory or callbacks")
        return await self._operate(
            lambda control: observe_preparation(
                settings.on_event,
                lambda events: _run_prepared(
                    self, connection_factory, prepare, handler, settings, control, events, policy
                ),
            ),
            stop_event,
        )

    async def _stage(
        self,
        connection: Any,
        handler: Callable[[Any], Awaitable[bytes | str]] | None,
        control: Control,
        hooks: _EventConsumerTransactionHooks | None = None,
        policy: EventAcknowledgmentIntentStoredRetryPolicy | None = None,
    ) -> EventAcknowledgmentIntentResult:
        intent = None

        async def missing(_cursor: Any) -> bytes:
            raise EventAcknowledgmentWorkerError("event_consumer_receipt_missing")

        async def save(cursor: Any, receipt: EventConsumerTransactionResult) -> None:
            nonlocal intent
            intent = await _save_completion(
                cursor,
                self._input,
                self._delivery.invocation_id,
                self._delivery.replay_generation,
                self._delivery.attempt,
                self._slug,
                self._seconds,
                self._token,
                receipt,
                control,
                policy,
            )

        unknown = False
        try:
            # A receipt-only probe must report missing without consulting a local claim.
            selected_hooks = (
                _EventConsumerTransactionHooks(hooks.storage_error) if hooks is not None and handler is None else hooks
            )
            receipt = await self._transaction(
                connection, missing if handler is None else handler, control, save, selected_hooks
            )
        except EventConsumerCommitUnknownError:
            unknown = True
        if unknown:
            # Keep the category/uncertainty without retaining driver parameters as a cause.
            raise EventConsumerCommitUnknownError(
                "event consumer/intent commit outcome unknown; reconcile on a fresh connection"
            )
        control.result = EventAcknowledgmentWorkerResult(True, receipt)
        return EventAcknowledgmentIntentResult(receipt, intent)

    async def retry(
        self, connection: Any, reason: str, *, stop_event: asyncio.Event | None = None
    ) -> EventAcknowledgmentWorkerResult:
        """Persist this decision first; requires stopped work, known rollback and exclusive job ownership."""
        return await self._reject(connection, "retry", reason, stop_event)

    async def save_retry_intent(
        self,
        connection: Any,
        reason: str,
        *,
        stop_event: asyncio.Event | None = None,
        retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None,
        retry_budget: EventAcknowledgmentIntentRetryBudget | None = None,
    ) -> EventAcknowledgmentIntentStatus:
        """Persist an explicit decision after known rollback, stopped work and exclusive ownership."""
        policy = _staging_policy(retry_policy, retry_budget)
        return await self._save_rejection(connection, "retry", reason, stop_event, policy)

    async def save_dead_letter_intent(
        self,
        connection: Any,
        reason: str,
        *,
        stop_event: asyncio.Event | None = None,
        retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None,
        retry_budget: EventAcknowledgmentIntentRetryBudget | None = None,
    ) -> EventAcknowledgmentIntentStatus:
        """Same preconditions as save_retry_intent. Sends no callback or platform renewal."""
        policy = _staging_policy(retry_policy, retry_budget)
        return await self._save_rejection(connection, "dead-letter", reason, stop_event, policy)

    async def _save_rejection(
        self,
        connection: Any,
        action: str,
        reason: str,
        stop_event: asyncio.Event | None,
        policy: EventAcknowledgmentIntentStoredRetryPolicy | None = None,
    ) -> EventAcknowledgmentIntentStatus:
        if not reason_code(reason):
            raise ValueError("invalid event acknowledgment failure reason")
        return await self._operate(
            lambda control: _save_negative(
                connection,
                self._input,
                self._delivery,
                self._slug,
                self._seconds,
                self._token,
                action,
                reason,
                control,
                policy,
            ),
            stop_event,
        )

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

    async def _receipt(
        self, connection: Any, control: Control, hooks: _EventConsumerTransactionHooks | None = None
    ) -> EventConsumerTransactionResult:
        async def missing(_cursor: Any) -> bytes:
            raise EventAcknowledgmentWorkerError("event_consumer_receipt_missing")

        probe_hooks = _EventConsumerTransactionHooks(hooks.storage_error) if hooks is not None else None
        return await self._transaction(connection, missing, control, hooks=probe_hooks)

    async def _transaction(
        self,
        connection: Any,
        handler: Callable[[Any], Awaitable[bytes | str]],
        control: Control,
        before_commit: Callable[[Any, EventConsumerTransactionResult], Awaitable[None]] | None = None,
        hooks: _EventConsumerTransactionHooks | None = None,
    ) -> EventConsumerTransactionResult:
        try:
            return await _awith_event_consumer_transaction(
                connection, self._input, handler, control.check, before_commit, hooks
            )
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

    async def _monitor(self, control: Control, on_failure: Callable[[BaseException, bool], None] | None = None) -> None:
        def stop(failure: EventAcknowledgmentWorkerError) -> None:
            already_stopped = control.event.is_set()
            control.stop(failure)
            if on_failure is not None:
                try:
                    on_failure(failure, already_stopped)
                except BaseException:
                    pass  # Optional diagnostics cannot alter renewal cancellation.

        failure = None
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
            failure = callback_error(error, control.result)
        except (asyncio.CancelledError, EventAcknowledgmentWorkerError):
            pass
        except Exception:
            failure = EventAcknowledgmentWorkerError("event_acknowledgment_unconfirmed")
        if failure is not None:
            stop(failure)

    async def _terminal(self, action: str, value: bytes | str, control: Control) -> EventAcknowledgmentWorkerResult:
        return await terminal(
            self._client, self._path, self._token, self._delivery, self._budget, action, value, control
        )

    async def _operate(
        self,
        operation: Callable[[Control], Awaitable[_T]],
        stop_event: asyncio.Event | None,
    ) -> _T:
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
        try:
            return await owned_operation(operation, stop_event)
        finally:
            self._busy = False
