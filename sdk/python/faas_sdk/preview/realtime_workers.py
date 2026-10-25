"""Async consumer-group workers. Optional receipt-backed retries apply only to terminal settlements."""

from __future__ import annotations

import asyncio
import inspect
import json
import math
import re
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Literal, TypeVar
from uuid import UUID, uuid4

import httpx
from attrs import evolve

from . import errors
from .api.realtime import (
    claim_realtime_consumer_group_batch,
    claim_realtime_worker_group_batch,
    get_realtime_consumer_group,
    get_realtime_worker_group,
    heartbeat_realtime_consumer_group,
    heartbeat_realtime_worker_group,
    settle_realtime_consumer_group_batch,
    settle_realtime_consumer_group_claim,
    settle_realtime_worker_group_batch,
    settle_realtime_worker_group_claim,
)
from .client import AuthenticatedClient
from .models.realtime_consumer_group import RealtimeConsumerGroup
from .models.realtime_consumer_group_batch import RealtimeConsumerGroupBatch
from .models.realtime_consumer_group_batch_request import RealtimeConsumerGroupBatchRequest
from .models.realtime_consumer_group_batch_settlement_request import RealtimeConsumerGroupBatchSettlementRequest
from .models.realtime_consumer_group_batch_settlement_result import RealtimeConsumerGroupBatchSettlementResult
from .models.realtime_consumer_group_claim import RealtimeConsumerGroupClaim
from .models.realtime_consumer_group_message import RealtimeConsumerGroupMessage
from .models.realtime_consumer_group_settlement import RealtimeConsumerGroupSettlement
from .models.realtime_consumer_group_settlement_action import RealtimeConsumerGroupSettlementAction
from .models.realtime_consumer_group_settlement_result import RealtimeConsumerGroupSettlementResult
from .models.realtime_worker_batch_claim_request import RealtimeWorkerBatchClaimRequest
from .models.realtime_worker_heartbeat import RealtimeWorkerHeartbeat
from .models.realtime_worker_heartbeat_request import RealtimeWorkerHeartbeatRequest
from .types import UNSET

T = TypeVar("T")
Handler = Callable[[RealtimeConsumerGroupMessage, "RealtimeWorkerContext"], Awaitable[None]]
_RETRY_CODES = {"realtime_group_rate_limited", "realtime_group_circuit_open", "realtime_group_deduplication_full"}


class RealtimeWorkerError(Exception):
    """Worker/request failure. HTTP failures exclude response bodies and lease secrets."""

    def __init__(
        self,
        message: str,
        *,
        status_code: int = 0,
        code: str = "worker_error",
        retry_after_seconds: float | None = None,
    ):
        super().__init__(message)
        self.status_code = status_code
        self.code = code
        self.retry_after_seconds = retry_after_seconds


class RealtimeLeaseLostError(RealtimeWorkerError):
    """Local expiry, delivery deadline, or failed renewal made continued processing unsafe."""


@dataclass(frozen=True, kw_only=True)
class RealtimeWorkerEvent:
    """Secret-free lifecycle notification; durations use the monotonic clock."""

    kind: Literal["claimed", "settled", "lease_lost", "handler_timeout", "draining", "shutdown"]
    group_id: UUID | None = None
    sequence: str | None = None
    attempt: int | None = None
    duration_seconds: float | None = None
    action: Literal["complete", "retry", "release"] | None = None
    status: Literal["completed", "ready", "dead"] | None = None
    reason: Literal["stopped", "cancelled", "failed", "blocked", "drained"] | None = None


@dataclass(frozen=True, kw_only=True)
class RealtimeWorkerOptions:
    concurrency: int = 1
    batch_size: int = 1
    wait_seconds: int = 10
    poll_interval_seconds: float = 1.0
    processing_timeout_seconds: float | None = None
    settlement_retries: int = 0
    settlement_batch_size: int = 1
    settlement_flush_milliseconds: int = 10
    heartbeat: bool = False
    release_on_shutdown: bool = False
    use_group_retry_policy: bool = False
    retry_after_seconds: int | None = None
    on_event: Callable[[RealtimeWorkerEvent], None] | None = None
    failure_code: Callable[[Exception, RealtimeConsumerGroupMessage], str] | None = None
    on_handler_error: Callable[[Exception, RealtimeConsumerGroupMessage], Awaitable[None]] | None = None

    def validate(self) -> None:
        for name, value, low, high in (
            ("concurrency", self.concurrency, 1, 32),
            ("batch_size", self.batch_size, 1, 16),
            ("wait_seconds", self.wait_seconds, 0, 20),
            ("settlement_retries", self.settlement_retries, 0, 3),
            ("settlement_batch_size", self.settlement_batch_size, 1, 32),
            ("settlement_flush_milliseconds", self.settlement_flush_milliseconds, 1, 100),
        ):
            if type(value) is not int or not low <= value <= high:
                raise ValueError(f"{name} must be an integer between {low} and {high}")
        value = self.poll_interval_seconds
        if (
            isinstance(value, bool)
            or not isinstance(value, (int, float))
            or not 0.1 <= value <= 60
            or not math.isfinite(value)
        ):
            raise ValueError("poll_interval_seconds must be between 0.1 and 60")
        timeout = self.processing_timeout_seconds
        if timeout is not None and (
            isinstance(timeout, bool)
            or not isinstance(timeout, (int, float))
            or not math.isfinite(timeout)
            or not 0.1 <= timeout <= 86400
        ):
            raise ValueError("processing_timeout_seconds must be between 0.1 and 86400, or None")
        if self.retry_after_seconds is not None and (
            type(self.retry_after_seconds) is not int or not 0 <= self.retry_after_seconds <= 300
        ):
            raise ValueError("retry_after_seconds must be an integer between 0 and 300")
        for name in ("heartbeat", "release_on_shutdown", "use_group_retry_policy"):
            if type(getattr(self, name)) is not bool:
                raise ValueError(f"{name} must be a boolean")
        for callback in (self.failure_code, self.on_handler_error, self.on_event):
            if callback is not None and not callable(callback):
                raise ValueError("worker callbacks must be callable")


@dataclass(frozen=True)
class RealtimeWorkerContext:
    """Handlers must observe stop_event and await all side effects before returning.

    claim exposes the group UUID/fence for downstream stale-write protection.
    The claim token is sensitive; do not log or persist the claim in generic caches.
    """

    claim: RealtimeConsumerGroupClaim = field(repr=False)
    stop_event: asyncio.Event = field(repr=False)


class _Stopped(Exception):
    pass


async def _cancel_join(*tasks: asyncio.Task[Any]) -> None:
    """Join cancelled requests/timers even if their owner is cancelled repeatedly."""
    for task in tasks:
        if not task.done():
            task.cancel()
    joined = asyncio.gather(*tasks, return_exceptions=True)
    interrupted = False
    while not joined.done():
        try:
            await asyncio.shield(joined)
        except asyncio.CancelledError:
            interrupted = True
    joined.result()
    if interrupted:
        raise asyncio.CancelledError()


def _http_error(status: int, content: bytes) -> RealtimeWorkerError:
    code = "http_error"
    retry = None
    try:
        body = json.loads(content)
        if isinstance(body, dict):
            candidate = body.get("code")
            if isinstance(candidate, str) and re.fullmatch(r"[a-z0-9_]{1,128}", candidate):
                code = candidate
            hint = body.get("retry_after_seconds")
            if type(hint) in (int, float) and math.isfinite(hint) and hint > 0:
                retry = float(hint)
    except (ValueError, OverflowError):
        pass
    return RealtimeWorkerError(
        f"Realtime request failed: HTTP {status} ({code})", status_code=status, code=code, retry_after_seconds=retry
    )


@dataclass
class _WorkerAPI:
    client: AuthenticatedClient = field(repr=False)
    target: dict[str, str]
    worker: str | None = None
    settlement_retries: int = 0

    @property
    def scoped(self) -> bool:
        return "group_id" in self.target

    async def _call(self, module: Any, expected: type[T], **kwargs: Any) -> T:
        try:
            response = await module.asyncio_detailed(client=self.client, **self.target, **kwargs)
        except errors.UnexpectedStatus as error:
            raise _http_error(error.status_code, error.content) from None
        except (KeyError, TypeError, ValueError):
            raise RealtimeWorkerError("Invalid realtime response") from None
        if response.status_code != 200:
            raise _http_error(int(response.status_code), response.content)
        if not isinstance(response.parsed, expected):
            raise RealtimeWorkerError("Missing or invalid realtime response")
        return response.parsed

    async def status(self) -> RealtimeConsumerGroup:
        return await self._call(
            get_realtime_worker_group if self.scoped else get_realtime_consumer_group, RealtimeConsumerGroup
        )

    async def claim(self, limit: int, wait: int) -> RealtimeConsumerGroupBatch:
        if self.scoped:
            return await self._call(
                claim_realtime_worker_group_batch,
                RealtimeConsumerGroupBatch,
                body=RealtimeWorkerBatchClaimRequest(limit=limit, wait_seconds=wait),
            )
        return await self._call(
            claim_realtime_consumer_group_batch,
            RealtimeConsumerGroupBatch,
            body=RealtimeConsumerGroupBatchRequest(worker=self.worker or "", limit=limit, wait_seconds=wait),
        )

    async def settle(self, body: RealtimeConsumerGroupSettlement) -> RealtimeConsumerGroupSettlementResult:
        retries = self.settlement_retries if body.action != "renew" else 0
        if retries:
            body = evolve(body, idempotency_key=uuid4() if body.idempotency_key is UNSET else body.idempotency_key)
        return await self._settlement_call(
            settle_realtime_worker_group_claim if self.scoped else settle_realtime_consumer_group_claim,
            RealtimeConsumerGroupSettlementResult,
            body,
            retries,
        )

    async def settle_batch(
        self, bodies: list[RealtimeConsumerGroupSettlement]
    ) -> RealtimeConsumerGroupBatchSettlementResult:
        body = RealtimeConsumerGroupBatchSettlementRequest(
            settlements=bodies,
            idempotency_key=uuid4() if self.settlement_retries else UNSET,
        )
        return await self._settlement_call(
            settle_realtime_worker_group_batch if self.scoped else settle_realtime_consumer_group_batch,
            RealtimeConsumerGroupBatchSettlementResult,
            body,
            self.settlement_retries,
        )

    async def _settlement_call(self, module: Any, expected: type[T], body: Any, retries: int) -> T:
        # One immutable logical request/key across all transport attempts.
        for attempt in range(retries + 1):
            try:
                return await self._call(module, expected, body=body)
            except (httpx.NetworkError, httpx.TimeoutException, httpx.RemoteProtocolError):
                if attempt == retries:
                    raise
            except RealtimeWorkerError as error:
                if attempt == retries or error.status_code not in (408, 500, 502, 503, 504):
                    raise
            await asyncio.sleep(0.1 * 2**attempt)
        raise AssertionError("Unreachable settlement retry state")

    async def heartbeat(self) -> RealtimeWorkerHeartbeat:
        return await self._call(
            heartbeat_realtime_worker_group if self.scoped else heartbeat_realtime_consumer_group,
            RealtimeWorkerHeartbeat,
            body=RealtimeWorkerHeartbeatRequest(ready=True, worker=self.worker or UNSET, status_code="ready"),
        )


def _timestamp(value: datetime) -> float:
    if not isinstance(value, datetime) or value.tzinfo is None or value.utcoffset() is None:
        raise RealtimeLeaseLostError("Invalid lease or delivery deadline")
    stamp = value.timestamp()
    if not math.isfinite(stamp):
        raise RealtimeLeaseLostError("Invalid lease or delivery deadline")
    return stamp


class _Runner:
    def __init__(
        self,
        api: _WorkerAPI,
        handler: Handler,
        options: RealtimeWorkerOptions,
        stop_event: asyncio.Event | None,
        drain_event: asyncio.Event | None = None,
    ):
        options.validate()
        if not callable(handler):
            raise ValueError("on_message must be an async callable")
        self.api, self.handler, self.options, self.stop_event = api, handler, options, stop_event
        self.api.settlement_retries = options.settlement_retries
        self.halt = asyncio.Event()
        self.drain_event = drain_event
        self.draining = asyncio.Event()
        self.failure: Exception | None = None
        self.release_error: Exception | None = None
        self.graceful = False
        self.observed_group_id: UUID | None = None
        self.active: set[asyncio.Task[None]] = set()
        self.pending: list[
            tuple[RealtimeConsumerGroupSettlement, asyncio.Future[RealtimeConsumerGroupSettlementResult]]
        ] = []
        self.batch_wake = asyncio.Event()
        self.batch_task: asyncio.Task[None] | None = None

    def _emit(self, event: RealtimeWorkerEvent) -> None:
        callback = self.options.on_event
        if callback is None:
            return
        try:
            result = callback(event)
            # Hooks are synchronous; avoid leaking an accidentally returned coroutine.
            if inspect.iscoroutine(result):
                result.close()
        except (Exception, asyncio.CancelledError):
            # Monitoring failures must not change delivery or cancellation semantics.
            pass

    def _begin_drain(self) -> None:
        if not self.draining.is_set():
            self.draining.set()
            self.batch_wake.set()
            self._emit(RealtimeWorkerEvent(kind="draining", group_id=self.observed_group_id))

    def _stop(self) -> None:
        if self.drain_event is not None and self.drain_event.is_set():
            self._begin_drain()
        if self.stop_event is not None and self.stop_event.is_set():
            self.graceful = self.failure is None
            self.halt.set()

    def _fail(self, error: Exception) -> None:
        if self.failure is None:
            self.failure = error
        self.graceful = False
        self.halt.set()

    async def _request(self, factory: Callable[[], Awaitable[T]]) -> T:
        self._stop()
        if self.halt.is_set():
            raise _Stopped()
        request = asyncio.create_task(factory())
        stopped = asyncio.create_task(self.halt.wait())
        try:
            done, _ = await asyncio.wait((request, stopped), return_when=asyncio.FIRST_COMPLETED)
            # Keep a received claim even if shutdown wins the same event-loop turn.
            if request in done:
                if request.cancelled():
                    if self.halt.is_set():
                        raise _Stopped()
                    raise RealtimeWorkerError("Realtime request cancelled unexpectedly")
                return request.result()
            raise _Stopped()
        finally:
            await _cancel_join(request, stopped)

    async def _wait(self, seconds: float | None, *, wake_on_handler: bool = False, wake_on_drain: bool = False) -> None:
        stopped = asyncio.create_task(self.halt.wait())
        drained = asyncio.create_task(self.draining.wait()) if wake_on_drain else None
        try:
            tasks = {stopped, *self.active} if wake_on_handler else {stopped}
            if drained is not None:
                tasks.add(drained)
            await asyncio.wait(tasks, timeout=seconds, return_when=asyncio.FIRST_COMPLETED)
        finally:
            await _cancel_join(stopped, *([drained] if drained is not None else []))

    async def _observe_stop(self) -> None:
        if self.stop_event is not None:
            await self.stop_event.wait()
            self._stop()

    async def _observe_drain(self) -> None:
        if self.drain_event is not None:
            await self.drain_event.wait()
            self._begin_drain()

    async def _heartbeat(self) -> None:
        try:
            while not self.halt.is_set():
                await self._wait(15)
                if not self.halt.is_set():
                    await self._request(self.api.heartbeat)
        except _Stopped:
            pass
        except Exception as error:
            self._fail(error)

    def _finished(self, task: asyncio.Task[None]) -> None:
        self.active.discard(task)
        if task.cancelled():
            if not self.halt.is_set():
                self._fail(RealtimeWorkerError("Handler task cancelled unexpectedly"))
        elif (error := task.exception()) is not None:
            self._fail(error)

    async def _terminal(self, body: RealtimeConsumerGroupSettlement) -> RealtimeConsumerGroupSettlementResult:
        if self.options.settlement_batch_size == 1:
            return await self._request(lambda: self.api.settle(body))
        self._stop()
        if self.halt.is_set():
            raise _Stopped()
        future: asyncio.Future[RealtimeConsumerGroupSettlementResult] = asyncio.get_running_loop().create_future()
        self.pending.append((body, future))
        if len(self.pending) >= self.options.settlement_batch_size or self.draining.is_set():
            self.batch_wake.set()
        if self.batch_task is None or self.batch_task.done():
            self.batch_task = asyncio.create_task(self._flush_batches())
        return await future

    async def _flush_batches(self) -> None:
        while self.pending:
            wake = asyncio.create_task(self.batch_wake.wait())
            stop = asyncio.create_task(self.halt.wait())
            try:
                await asyncio.wait(
                    (wake, stop),
                    timeout=self.options.settlement_flush_milliseconds / 1000,
                    return_when=asyncio.FIRST_COMPLETED,
                )
            finally:
                await _cancel_join(wake, stop)
            self.batch_wake.clear()
            batch = self.pending[: self.options.settlement_batch_size]
            del self.pending[: len(batch)]
            try:
                if self.halt.is_set() and not self.graceful:
                    raise _Stopped()
                request = asyncio.create_task(self.api.settle_batch([body for body, _ in batch]))
                stop = asyncio.create_task(self.halt.wait())
                try:
                    # Bound an in-flight batch, including retries, during shutdown.
                    async with asyncio.timeout(5):
                        await asyncio.wait((request, stop), return_when=asyncio.FIRST_COMPLETED)
                        if self.halt.is_set() and not self.graceful:
                            raise _Stopped()
                        response = await request
                finally:
                    await _cancel_join(request, stop)
                if len(response.results) != len(batch) or any(
                    item.sequence != body.sequence
                    or item.action != body.action
                    or not (
                        item.status == "lease_lost"
                        or body.action == "complete"
                        and item.status == "completed"
                        or body.action == "retry"
                        and item.status in ("ready", "dead")
                    )
                    for item, (body, _) in zip(response.results, batch)
                ):
                    raise RealtimeWorkerError("Invalid batch settlement response")
                for item, (_, future) in zip(response.results, batch):
                    if future.done():
                        continue
                    if item.status == "lease_lost":
                        future.set_exception(RealtimeLeaseLostError("Lease lost during batch settlement"))
                    else:
                        future.set_result(
                            RealtimeConsumerGroupSettlementResult(
                                status=item.status,
                                available_at=item.available_at,
                                lease_until=item.lease_until,
                            )
                        )
            except Exception as error:
                if not isinstance(error, _Stopped):
                    self._fail(error)
                for _, future in batch:
                    if not future.done():
                        future.set_exception(error)

    async def _process(self, claim: RealtimeConsumerGroupClaim, lease_seconds: int, group_id: UUID) -> None:
        started = time.monotonic()
        reported_loss = False

        def notify_loss() -> None:
            nonlocal reported_loss
            if not reported_loss:
                reported_loss = True
                self._emit(
                    RealtimeWorkerEvent(
                        kind="lease_lost",
                        group_id=group_id,
                        sequence=claim.message.sequence,
                        attempt=claim.attempt,
                        duration_seconds=max(0.0, time.monotonic() - started),
                    )
                )

        try:
            await self._process_claim(claim, lease_seconds, group_id, started, notify_loss)
        except RealtimeLeaseLostError:
            notify_loss()
            raise

    async def _process_claim(
        self,
        claim: RealtimeConsumerGroupClaim,
        lease_seconds: int,
        group_id: UUID,
        started: float,
        notify_loss: Callable[[], None],
    ) -> None:
        if claim.group_id != group_id or not claim.claim_token:
            raise RealtimeLeaseLostError("Claim does not belong to the observed consumer group")
        until = _timestamp(claim.lease_until)
        deadline = _timestamp(claim.delivery_deadline) if claim.delivery_deadline is not None else math.inf
        self._emit(
            RealtimeWorkerEvent(
                kind="claimed",
                group_id=group_id,
                sequence=claim.message.sequence,
                attempt=claim.attempt,
            )
        )
        context = RealtimeWorkerContext(claim, asyncio.Event())
        lease_error: Exception | None = None

        def lose(error: Exception) -> None:
            nonlocal lease_error
            lease_error = error
            notify_loss()
            context.stop_event.set()
            self._fail(error)

        async def watch() -> None:
            while not self.halt.is_set():
                remaining = min(until, deadline) - time.time()
                if remaining <= 0:
                    lose(RealtimeLeaseLostError("Lease or delivery deadline reached"))
                    return
                await self._wait(min(remaining, 60))
            context.stop_event.set()

        async def renew() -> None:
            nonlocal until
            try:
                while not self.halt.is_set():
                    await self._wait(max(0.1, lease_seconds / 3))
                    if self.halt.is_set():
                        return
                    result = await self._request(
                        lambda: self.api.settle(
                            RealtimeConsumerGroupSettlement(
                                sequence=claim.message.sequence, claim_token=claim.claim_token, action="renew"
                            )
                        )
                    )
                    if result.status != "leased" or not isinstance(result.lease_until, datetime):
                        raise RealtimeLeaseLostError("Invalid lease renewal response")
                    until = _timestamp(result.lease_until)
                    if min(until, deadline) <= time.time():
                        raise RealtimeLeaseLostError("Renewed lease or delivery deadline reached")
            except _Stopped:
                pass
            except Exception as error:
                if isinstance(error, RealtimeLeaseLostError):
                    lose(error)
                else:
                    lost = RealtimeLeaseLostError(
                        "Lease renewal failed",
                        status_code=error.status_code if isinstance(error, RealtimeWorkerError) else 0,
                        code=error.code if isinstance(error, RealtimeWorkerError) else "lease_renewal_failed",
                    )
                    lost.__cause__ = error
                    lose(lost)

        renewal = asyncio.create_task(renew())
        monitors = [asyncio.create_task(watch()), renewal]
        timed_out = False
        handler_started: float | None = None

        def signal_timeout() -> None:
            nonlocal timed_out
            if timed_out or self.halt.is_set() or lease_error is not None:
                return
            timed_out = True
            context.stop_event.set()
            renewal.cancel()
            self._emit(
                RealtimeWorkerEvent(
                    kind="handler_timeout",
                    group_id=group_id,
                    sequence=claim.message.sequence,
                    attempt=claim.attempt,
                    duration_seconds=max(0.0, time.monotonic() - started),
                )
            )

        async def processing_timer() -> None:
            assert handler_started is not None and self.options.processing_timeout_seconds is not None
            remaining = self.options.processing_timeout_seconds - (time.monotonic() - handler_started)
            await self._wait(max(0.0, remaining))
            signal_timeout()
            if timed_out:
                await _cancel_join(renewal)

        handler_error: Exception | None = None
        try:
            self._stop()
            if self.halt.is_set() or min(until, deadline) <= time.time():
                context.stop_event.set()
            if min(until, deadline) <= time.time():
                lose(RealtimeLeaseLostError("Claim already expired"))
            if not context.stop_event.is_set():
                handler_started = time.monotonic()
                if self.options.processing_timeout_seconds is not None:
                    monitors.append(asyncio.create_task(processing_timer()))
                try:
                    await self.handler(claim.message, context)
                except asyncio.CancelledError:
                    if not self.halt.is_set() and not timed_out:
                        raise RealtimeWorkerError("Handler cancelled before shutdown") from None
                except Exception as error:
                    handler_error = error
                # Account for handlers that return after blocking the event loop past the budget.
                if (
                    self.options.processing_timeout_seconds is not None
                    and handler_started is not None
                    and time.monotonic() - handler_started >= self.options.processing_timeout_seconds
                ):
                    signal_timeout()
                if timed_out:
                    handler_error = TimeoutError("Realtime handler processing timeout")
        finally:
            await _cancel_join(*monitors)
        self._stop()
        if self.halt.is_set():
            # Handler and monitors have stopped before this independent release request.
            if (
                self.options.release_on_shutdown
                and self.graceful
                and self.failure is None
                and lease_error is None
                and min(until, deadline) > time.time()
            ):
                try:
                    async with asyncio.timeout(5):
                        released = await self.api.settle(
                            RealtimeConsumerGroupSettlement(
                                sequence=claim.message.sequence, claim_token=claim.claim_token, action="release"
                            )
                        )
                        if released.status != "ready":
                            raise RealtimeWorkerError("Invalid lease release response")
                        self._emit(
                            RealtimeWorkerEvent(
                                kind="settled",
                                group_id=group_id,
                                sequence=claim.message.sequence,
                                attempt=claim.attempt,
                                duration_seconds=max(0.0, time.monotonic() - started),
                                action="release",
                                status="ready",
                            )
                        )
                except Exception as error:
                    if self.release_error is None:
                        self.release_error = error
            return
        if min(until, deadline) <= time.time():
            raise RealtimeLeaseLostError("Lease or delivery deadline reached before settlement")
        action: RealtimeConsumerGroupSettlementAction = "retry" if handler_error is not None else "complete"
        body = RealtimeConsumerGroupSettlement(
            sequence=claim.message.sequence, claim_token=claim.claim_token, action=action
        )
        if handler_error is not None:
            code = (
                "handler_timeout"
                if timed_out
                else (
                    self.options.failure_code(handler_error, claim.message)
                    if self.options.failure_code
                    else "handler_failed"
                )
            )
            if not isinstance(code, str) or re.fullmatch(r"[\x20-\x7e]{1,64}", code) is None:
                raise ValueError("failure_code must contain 1–64 printable ASCII characters")
            body.failure_code = code
            if not self.options.use_group_retry_policy or self.options.retry_after_seconds is not None:
                body.retry_after_seconds = (
                    self.options.retry_after_seconds if self.options.retry_after_seconds is not None else 1
                )
        try:
            settled = await self._terminal(body)
            if (action == "complete" and settled.status != "completed") or (
                action == "retry" and settled.status not in ("ready", "dead")
            ):
                raise RealtimeWorkerError("Invalid terminal settlement response")
        except _Stopped:
            # An uncertain terminal settlement is never followed by release or replay.
            return
        self._emit(
            RealtimeWorkerEvent(
                kind="settled",
                group_id=group_id,
                sequence=claim.message.sequence,
                attempt=claim.attempt,
                duration_seconds=max(0.0, time.monotonic() - started),
                action=action,
                status=settled.status,
            )
        )
        if handler_error is not None and self.options.on_handler_error is not None:
            await self.options.on_handler_error(handler_error, claim.message)

    async def run(self) -> None:
        self._stop()
        if self.halt.is_set() or self.draining.is_set():
            self._emit(RealtimeWorkerEvent(kind="shutdown", reason="stopped" if self.halt.is_set() else "drained"))
            return
        observer = asyncio.create_task(self._observe_stop())
        drain_observer = asyncio.create_task(self._observe_drain())
        heartbeat: asyncio.Task[None] | None = None
        cancelled = False
        blocker: RealtimeWorkerError | None = None
        try:
            group = await self._request(self.api.status)
            if type(group.lease_seconds) is not int or not 1 <= group.lease_seconds <= 300:
                raise RealtimeWorkerError("Invalid consumer-group lease duration")
            try:
                observed_group_id = UUID(group.group_id)
            except (ValueError, TypeError, AttributeError):
                raise RealtimeWorkerError("Invalid consumer-group identity") from None
            self.observed_group_id = observed_group_id
            if self.api.scoped and str(observed_group_id) != self.api.target["group_id"]:
                raise RealtimeWorkerError("Scoped worker group identity changed")
            if self.options.heartbeat:
                await self._request(self.api.heartbeat)
                heartbeat = asyncio.create_task(self._heartbeat())
            while not self.halt.is_set():
                self._stop()
                if self.halt.is_set():
                    break
                if self.draining.is_set():
                    if not self.active:
                        break
                    self.batch_wake.set()
                    await self._wait(None, wake_on_handler=True)
                    continue
                if blocker is not None:
                    if not self.active:
                        break
                    await self._wait(None, wake_on_handler=True, wake_on_drain=True)
                    continue
                slots = self.options.concurrency - len(self.active)
                if slots == 0:
                    await self._wait(None, wake_on_handler=True, wake_on_drain=True)
                    continue
                try:
                    batch = await self._request(
                        lambda: self.api.claim(min(slots, self.options.batch_size), self.options.wait_seconds)
                    )
                except RealtimeWorkerError as error:
                    if error.status_code != 429 or error.code not in _RETRY_CODES or error.retry_after_seconds is None:
                        raise
                    await self._wait(
                        min(
                            error.retry_after_seconds,
                            self.options.poll_interval_seconds
                            if error.code == "realtime_group_deduplication_full"
                            else 60,
                        ),
                        wake_on_drain=True,
                    )
                    continue
                if len(batch.claims) > min(slots, self.options.batch_size):
                    raise RealtimeWorkerError("Claim batch exceeds available handler slots")
                if batch.history_unavailable:
                    blocker = RealtimeWorkerError(
                        "Consumer-group retention gap requires explicit recovery", code="history_unavailable"
                    )
                elif batch.capacity_blocked:
                    blocker = RealtimeWorkerError(
                        "Consumer-group outstanding snapshot capacity reached", code="capacity_blocked"
                    )
                for claim in batch.claims:
                    task = asyncio.create_task(self._process(claim, group.lease_seconds, observed_group_id))
                    self.active.add(task)
                    task.add_done_callback(self._finished)
                if blocker is None and (batch.rate_throttled or batch.circuit_open or batch.deduplication_blocked):
                    hint = batch.retry_after_milliseconds
                    if type(hint) not in (int, float) or not math.isfinite(hint) or hint <= 0:
                        raise RealtimeWorkerError("Invalid consumer-group claim retry hint")
                    await self._wait(
                        min(hint / 1000, self.options.poll_interval_seconds if batch.deduplication_blocked else 60),
                        wake_on_handler=batch.deduplication_blocked,
                        wake_on_drain=True,
                    )
                elif blocker is None and not batch.claims:
                    await self._wait(self.options.poll_interval_seconds, wake_on_handler=True, wake_on_drain=True)
        except _Stopped:
            pass
        except asyncio.CancelledError:
            cancelled = True
            self.graceful = self.failure is None
        except Exception as error:
            self._fail(error)
        finally:
            self.halt.set()

            async def finish() -> None:
                observer.cancel()
                drain_observer.cancel()
                if heartbeat is not None:
                    heartbeat.cancel()
                if self.pending:
                    self.batch_wake.set()
                await asyncio.gather(
                    observer,
                    drain_observer,
                    *([heartbeat] if heartbeat is not None else []),
                    *tuple(self.active),
                    *([self.batch_task] if self.batch_task is not None else []),
                    return_exceptions=True,
                )

            cleanup = asyncio.create_task(finish())
            while not cleanup.done():
                try:
                    await asyncio.shield(cleanup)
                except asyncio.CancelledError:
                    cancelled = True
                    self.graceful = self.failure is None
            cleanup.result()
            self._emit(
                RealtimeWorkerEvent(
                    kind="shutdown",
                    group_id=self.observed_group_id,
                    reason=(
                        "failed"
                        if self.failure is not None or self.release_error is not None
                        else "cancelled"
                        if cancelled
                        else "blocked"
                        if blocker is not None and not self.graceful
                        else "drained"
                        if self.draining.is_set() and not self.graceful
                        else "stopped"
                    ),
                )
            )
        if self.release_error is not None:
            raise self.release_error
        if self.failure is not None:
            raise self.failure
        if cancelled:
            raise asyncio.CancelledError()
        if blocker is not None and not self.graceful:
            raise blocker


async def consume_realtime_consumer_group(
    client: AuthenticatedClient,
    *,
    slug: str,
    id: str,
    channel: str,
    group: str,
    worker: str,
    on_message: Handler,
    options: RealtimeWorkerOptions | None = None,
    stop_event: asyncio.Event | None = None,
    drain_event: asyncio.Event | None = None,
) -> None:
    """Run an account-key worker until drain, stop, task cancellation, or an infrastructure failure.

    The caller owns the client. Handler failures retry; network/auth/lease errors stop the worker.
    Configure HTTP timeouts above wait_seconds, and disable transport retries for claims/settlements.
    """
    if not worker:
        raise ValueError("worker is required for account-authenticated workers")
    await _Runner(
        _WorkerAPI(client, {"slug": slug, "id": id, "channel": channel, "group": group}, worker),
        on_message,
        options or RealtimeWorkerOptions(),
        stop_event,
        drain_event,
    ).run()


async def consume_realtime_consumer_group_with_credential(
    client: AuthenticatedClient,
    *,
    group_id: str,
    on_message: Handler,
    options: RealtimeWorkerOptions | None = None,
    stop_event: asyncio.Event | None = None,
    drain_event: asyncio.Event | None = None,
) -> None:
    """Run with a grw_ credential; its issued identity is used without an account key.

    Grants: status, claim, renew, complete, retry; optional heartbeat and release grants for those options.
    """
    group_id = str(UUID(group_id))
    await _Runner(
        _WorkerAPI(client, {"group_id": group_id}),
        on_message,
        options or RealtimeWorkerOptions(),
        stop_event,
        drain_event,
    ).run()
