"""Typed producers and bounded workers over the existing Go application outbox."""

from __future__ import annotations

import asyncio
import math
import re
import secrets
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, replace
from datetime import UTC
from email.utils import parsedate_to_datetime
from typing import Any, Generic, Literal, TypeVar

from ._event_outbox_protocol import SQL
from .event_contract import EventContract
from .event_outbox_state import (
    DAY_MS,
    EventOutboxError,
    EventOutboxOutcome,
    EventOutboxRetryPolicy,
    EventOutboxStatus,
    EventPublicationOptions,
    EventPublicationOutcome,
    _hash,
    _instant,
    _integer,
    _key,
    _milliseconds,
    _parse_request,
    _policy_digest,
    _receipt,
    _scope_value,
    _slug,
    event_outbox_retry_delay,
    prepare_event_publication,
)
from .event_outbox_transaction import (
    EventOutboxConnection,
    EventOutboxPool,
    EventOutboxTransaction,
    with_event_outbox_transaction,
)
from .event_publication import _EventTransport, _HTTPResult, _operation, _publication_outcome
from .pipeline_state import PipelineScope

E = TypeVar("E")
T = TypeVar("T")


class EventPublisher(Generic[E]):
    def __init__(
        self,
        contract: EventContract[E],
        *,
        scope: PipelineScope,
        token: str | None = None,
        receipt_retention: float = 86400,
        httpx_args: dict[str, Any] | None = None,
    ):
        self._contract, self._scope = contract, _scope_value(scope)
        self._retention = _milliseconds(receipt_retention, 1000, 14 * DAY_MS)
        self._token, self._httpx_args = token, dict(httpx_args or {})

    async def stage(
        self,
        transaction: EventOutboxTransaction,
        value: E,
        options: EventPublicationOptions,
        *,
        retry_window: float = 86400,
        retry_policy: EventOutboxRetryPolicy | None = None,
    ) -> EventOutboxStatus:
        if not isinstance(transaction, EventOutboxTransaction):
            raise EventOutboxError("invalid_transaction")
        try:
            transaction._valid()
            body = prepare_event_publication(self._contract, self._scope, value, options)
            window = _milliseconds(retry_window, 1000, min(7 * DAY_MS, self._retention))
            args = [self._scope.account_id, self._scope.app_id, options.key]
            digest = _hash(body)
            await transaction._query(SQL["stage"], [*args, self._scope.app_slug, digest, body, window])
            old = await transaction._query(SQL["stage_check"], args)
            if len(old) != 1 or old[0]["app_slug"] != self._scope.app_slug or bytes(old[0]["request_digest"]) != digest:
                raise EventOutboxError("conflict")
            rows = await transaction._query(SQL["status"], args)
            if len(rows) != 1:
                raise EventOutboxError("store_failed")
            status = EventOutboxStatus.from_dict(rows[0])
            if retry_policy is not None:
                initial, maximum, attempts = retry_policy.milliseconds()
                policy_digest = _policy_digest(retry_policy)
                await transaction._query(SQL["policy_stage"], [*args, initial, maximum, attempts, policy_digest])
                saved = await transaction._query(SQL["policy"], args)
                if len(saved) != 1:
                    raise EventOutboxError("conflict")
                _stored_policy(saved[0])
                if bytes(saved[0]["initial_digest"]) != policy_digest:
                    raise EventOutboxError("conflict")
            return status
        except Exception as error:
            stopped = error if isinstance(error, EventOutboxError) else EventOutboxError("store_failed")
            transaction._fail(stopped)
            raise stopped from None

    async def publish(self, value: E, options: EventPublicationOptions, *, timeout: float) -> EventPublicationOutcome:
        body = prepare_event_publication(self._contract, self._scope, value, options)
        if self._token is None:
            raise EventOutboxError("invalid_request")
        response = await _EventTransport(self._scope, self._token, self._httpx_args).send(
            self._scope.app_slug, body, timeout=timeout
        )
        return _publication_outcome(response, self._scope, options.key)


def _stored_policy(row: dict[str, Any]) -> EventOutboxRetryPolicy:
    try:
        if (
            row["version"] != 1
            or not isinstance(row["revision"], str)
            or not re.fullmatch(r"[1-9][0-9]{0,18}", row["revision"])
            or not 0 < int(row["revision"]) <= 9223372036854775807
            or len(bytes(row["initial_digest"])) != 32
        ):
            raise ValueError
        initial = _integer(row["initial_backoff_ms"], 1, 3600000)
        maximum = _integer(row["max_backoff_ms"], initial, 3600000)
        return EventOutboxRetryPolicy(
            initial_backoff=initial / 1000,
            max_backoff=maximum / 1000,
            max_attempts=_integer(row["max_attempts"], 0, 2147483647),
        )
    except Exception:
        raise EventOutboxError("store_failed") from None


@dataclass(frozen=True, kw_only=True)
class EventOutboxRecoveryOptions:
    timeout: float
    claim_duration: float = 30
    request_timeout: float = 10
    backoff: float | None = None
    retry_policy: EventOutboxRetryPolicy | None = None


@dataclass(frozen=True, kw_only=True)
class EventOutboxReconciliation:
    outcome: Literal["published", "already_published", "unavailable", "unverified", "conflict"]
    publication: EventOutboxStatus


@dataclass(frozen=True)
class _Claim:
    key: str
    slug: str
    body: str
    token: bytes
    attempts: int
    policy: EventOutboxRetryPolicy | None


class EventOutbox:
    def __init__(
        self,
        pool: EventOutboxPool,
        *,
        scope: PipelineScope,
        token: str,
        receipt_retention: float = 86400,
        httpx_args: dict[str, Any] | None = None,
    ):
        self._pool, self._scope = pool, _scope_value(scope)
        self._retention = _milliseconds(receipt_retention, 1000, 14 * DAY_MS)
        self._transport = _EventTransport(self._scope, token, httpx_args)

    @property
    def _account(self) -> list[Any]:
        return [self._scope.account_id, self._scope.app_id]

    async def _using(
        self,
        check: Callable[[], None],
        work: Callable[[Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]], Awaitable[T]],
    ) -> T:
        connection: EventOutboxConnection | None = None
        try:
            check()
            connection = await self._pool.connect()
            check()
            owned = connection

            async def query(sql: str, values: list[Any]) -> list[dict[str, Any]]:
                check()
                try:
                    result = await owned.query(sql, values)
                    check()
                    return result
                except Exception:
                    check()
                    raise EventOutboxError("store_failed") from None

            return await work(query)
        except EventOutboxError:
            raise
        except Exception:
            check()
            raise EventOutboxError("store_failed") from None
        finally:
            if connection is not None:
                try:
                    await connection.release()
                except Exception:
                    raise EventOutboxError("store_failed") from None

    async def status(self, key: str, *, timeout: float) -> EventOutboxStatus | None:
        if not _key(key):
            raise EventOutboxError("invalid_request")

        async def work(check: Callable[[], None], remaining: Callable[[], float]) -> EventOutboxStatus | None:
            async def read(query: Any) -> EventOutboxStatus | None:
                rows = await query(SQL["status"], [*self._account, key])
                if len(rows) > 1:
                    raise EventOutboxError("store_failed")
                return EventOutboxStatus.from_dict(rows[0]) if rows else None

            return await self._using(check, read)

        return await _operation(timeout, work)

    async def recover_next(self, options: EventOutboxRecoveryOptions) -> EventOutboxOutcome:
        duration = _milliseconds(options.claim_duration, 1000, 300000)
        request_timeout = _milliseconds(options.request_timeout, 1, duration - 1)
        if options.retry_policy is not None and options.backoff is not None:
            raise EventOutboxError("invalid_request")
        if options.retry_policy is not None:
            options.retry_policy.milliseconds()
        backoff = _milliseconds(options.backoff if options.backoff is not None else 5, 1, 3600000)

        async def work(check: Callable[[], None], remaining: Callable[[], float]) -> EventOutboxOutcome:
            async def claim(transaction: EventOutboxTransaction) -> _Claim | EventOutboxOutcome:
                query = transaction._query
                await query(
                    SQL["publishing_lock"],
                    ["gregale.event-outbox-publishing.v1:" + self._scope.account_id + ":" + self._scope.app_id],
                )
                paused = await query(SQL["paused"], self._account)
                if len(paused) != 1 or type(paused[0]["paused"]) is not bool:
                    raise EventOutboxError("store_failed")
                if paused[0]["paused"]:
                    return EventOutboxOutcome(outcome="paused")
                rates = await query(SQL["rate"], self._account)
                if len(rates) > 1:
                    raise EventOutboxError("store_failed")
                rate = None
                if rates:
                    try:
                        r = rates[0]
                        if (
                            r["version"] != 1
                            or type(r["enabled"]) is not bool
                            or r["future"] is True
                            or not isinstance(r["revision"], str)
                            or not re.fullmatch(r"0|[1-9][0-9]{0,18}", r["revision"])
                            or not 0 <= int(r["revision"]) <= 9223372036854775807
                        ):
                            raise ValueError
                        interval = _integer(r["interval_ms"], 1, 3600000)
                        burst = _integer(r["burst"], 1, 10000)
                        _integer(r["credit_ms"], 0, interval * burst)
                        _integer(r["elapsed_ms"], 0, interval * burst)
                        credit = _integer(r["credit"], 0, interval * burst)
                        _instant(r["anchor"])
                        if r["enabled"] and credit < interval:
                            _instant(r["retry_at"])
                            return EventOutboxOutcome(
                                outcome="rate_limited",
                                retry_after_ms=_integer(r["retry_after_ms"], 1, 3600000),
                                retry_at=r["retry_at"],
                            )
                        if r["enabled"]:
                            rate = credit, interval, r["anchor"]
                    except Exception:
                        raise EventOutboxError("store_failed") from None
                candidates = await query(SQL["next"], self._account)
                if not candidates:
                    return EventOutboxOutcome(outcome="empty")
                if len(candidates) != 1:
                    raise EventOutboxError("store_failed")
                r = candidates[0]
                key = r["producer_key"]
                window = r["retry_window"]
                if (
                    not _key(key)
                    or not _slug(r["app_slug"])
                    or type(r["expired"]) is not bool
                    or type(window) not in (int, float)
                    or not math.isfinite(window)
                    or window <= 0
                ):
                    raise EventOutboxError("store_failed")
                if window * 1000 > self._retention + 0.001:
                    raise EventOutboxError("retention_mismatch")
                attempts = _integer(r["attempts"], 0, 2147483647)
                args = [*self._account, key]
                policies = await query(SQL["policy"], args)
                if len(policies) > 1:
                    raise EventOutboxError("store_failed")
                policy = _stored_policy(policies[0]) if policies else options.retry_policy
                code = (
                    "retry_window_expired"
                    if r["expired"]
                    else "attempts_exhausted"
                    if policy and policy.max_attempts and attempts >= policy.max_attempts
                    else ""
                )
                try:
                    if not isinstance(r["request_body"], str) or bytes(r["request_digest"]) != _hash(r["request_body"]):
                        raise ValueError
                    _parse_request(r["request_body"], self._scope.app_id, key)
                except Exception:
                    code = code or "publication_rejected"
                if code:
                    blocked = await query(SQL["block"], [*args, code])
                    return EventOutboxOutcome(outcome="blocked", status=self._one_status(blocked))
                token = secrets.token_bytes(32)
                claimed = await query(SQL["claim"], [*args, token, duration])
                if len(claimed) != 1:
                    raise EventOutboxError("claim_lost")
                if rate:
                    credit, interval, anchor = rate
                    await query(SQL["rate_consume"], [*self._account, credit - interval, anchor])
                return _Claim(
                    key,
                    r["app_slug"],
                    r["request_body"],
                    token,
                    _integer(claimed[0]["attempts"], 1, 2147483647),
                    policy,
                )

            selected = await with_event_outbox_transaction(self._pool, claim, timeout=remaining())
            check()
            if isinstance(selected, EventOutboxOutcome):
                return selected
            started = asyncio.get_running_loop().time()

            async def lease(query: Any) -> list[dict[str, Any]]:
                result: list[dict[str, Any]] = await query(SQL["lease"], [*self._account, selected.key, selected.token])
                return result

            rows = await self._using(check, lease)
            if len(rows) != 1:
                return EventOutboxOutcome(outcome="claim_lost")
            seconds = rows[0]["remaining"]
            if type(seconds) not in (int, float) or not math.isfinite(seconds) or not 0 < seconds <= 300:
                return EventOutboxOutcome(outcome="claim_lost")
            budget = min(request_timeout / 1000, remaining(), seconds - (asyncio.get_running_loop().time() - started))
            if budget < 0.001:
                return EventOutboxOutcome(outcome="claim_lost")
            try:
                response = await self._transport.send(selected.slug, selected.body, timeout=budget)
            except EventOutboxError as error:
                check()
                if error.code != "deadline":
                    raise
                response = _HTTPResult(status=0, value=None)
            check()
            publication = _publication_outcome(response, self._scope, selected.key)
            state = publication.state
            code = (
                "publication_confirmed"
                if state == "published"
                else "publication_rejected"
                if state == "blocked"
                else "unconfirmed"
            )
            if (
                state == "unconfirmed"
                and selected.policy
                and selected.policy.max_attempts
                and selected.attempts >= selected.policy.max_attempts
            ):
                state, code = "blocked", "attempts_exhausted"
            guidance, retry_at = _retry_guidance(response) if state == "unconfirmed" and selected.policy else (0, None)
            delay = event_outbox_retry_delay(selected.token, selected.attempts, selected.policy, backoff)

            async def settle(query: Any) -> EventOutboxOutcome:
                rows = await query(
                    SQL["settle"],
                    [
                        *self._account,
                        selected.key,
                        selected.token,
                        state,
                        code,
                        publication.receipt.accepted_at if publication.receipt is not None else None,
                        delay,
                        guidance,
                        retry_at,
                    ],
                )
                return (
                    EventOutboxOutcome(outcome=state, status=self._one_status(rows))
                    if rows
                    else EventOutboxOutcome(outcome="claim_lost")
                )

            return await self._using(check, settle)

        return await _operation(options.timeout, work)

    @staticmethod
    def _one_status(rows: list[dict[str, Any]]) -> EventOutboxStatus:
        if len(rows) != 1:
            raise EventOutboxError("claim_lost")
        return EventOutboxStatus.from_dict(rows[0])

    async def recover(
        self,
        options: EventOutboxRecoveryOptions,
        *,
        max_events: int,
        concurrency: int = 1,
        on_progress: Callable[[EventOutboxOutcome], Awaitable[None]] | None = None,
    ) -> tuple[EventOutboxOutcome, ...]:
        maximum, workers = _integer(max_events, 1, 10000), _integer(concurrency, 1, 32)

        async def work(check: Callable[[], None], remaining: Callable[[], float]) -> tuple[EventOutboxOutcome, ...]:
            started = 0
            outcomes = []

            async def worker() -> None:
                nonlocal started
                while started < maximum:
                    check()
                    started += 1
                    result = await self.recover_next(replace(options, timeout=remaining()))
                    check()
                    outcomes.append(result)
                    if on_progress is not None:
                        try:
                            await on_progress(result)
                            check()
                        except Exception:
                            check()
                            raise EventOutboxError("progress_failed") from None
                    if result.outcome in ("empty", "paused", "rate_limited", "claim_lost"):
                        break

            tasks = [asyncio.create_task(worker()) for _ in range(min(workers, maximum))]
            try:
                await asyncio.gather(*tasks)
            except BaseException:
                for task in tasks:
                    task.cancel()
                await asyncio.gather(*tasks, return_exceptions=True)
                raise
            return tuple(outcomes)

        return await _operation(options.timeout, work)

    async def reconcile(self, key: str, *, timeout: float, app_slug: str | None = None) -> EventOutboxReconciliation:
        if not _key(key) or app_slug is not None and not _slug(app_slug):
            raise EventOutboxError("invalid_request")

        async def work(check: Callable[[], None], remaining: Callable[[], float]) -> EventOutboxReconciliation:
            async def read(query: Any) -> EventOutboxReconciliation:
                rows = await query(SQL["reconcile_read"], [*self._account, key])
                if len(rows) != 1:
                    raise EventOutboxError("not_found")
                raw = rows[0]
                status = EventOutboxStatus.from_dict(raw)
                if status.state == "published":
                    return EventOutboxReconciliation(outcome="already_published", publication=status)
                if raw["live"] is not False:
                    raise EventOutboxError("busy")
                if not isinstance(raw["request_body"], str) or bytes(raw["request_digest"]) != _hash(
                    raw["request_body"]
                ):
                    raise EventOutboxError("conflict")
                _parse_request(raw["request_body"], self._scope.app_id, key)
                response = await self._transport.send(
                    app_slug or status.app_slug, raw["request_body"], verify=True, timeout=min(30, remaining())
                )
                check()
                unverified = EventOutboxReconciliation(outcome="unverified", publication=status)
                value = response.value
                if (
                    not 200 <= response.status < 300
                    or not isinstance(value, dict)
                    or value.get("app_id") != self._scope.app_id
                    or value.get("source") != "app." + self._scope.app_id
                    or value.get("event_id") != "key." + _hash(key).hex()
                ):
                    return unverified
                try:
                    _instant(value["observed_at"])
                except Exception:
                    return unverified
                if value.get("status") in ("unavailable", "conflict"):
                    return EventOutboxReconciliation(outcome=value["status"], publication=status)
                try:
                    receipt = _receipt(value | {"duplicate": False}, self._scope, key)
                    accepted = _instant(receipt.accepted_at)
                    if (
                        value.get("status") != "match"
                        or not _instant(status.created_at) <= accepted <= _instant(status.retry_until)
                        or accepted > _instant(value["observed_at"])
                    ):
                        return unverified
                except Exception:
                    return unverified
                settled = await query(
                    SQL["reconcile_settle"],
                    [
                        *self._account,
                        key,
                        status.state,
                        status.updated_at,
                        status.attempts,
                        raw["request_digest"],
                        status.app_slug,
                        receipt.accepted_at,
                        status.claim_expires_at,
                    ],
                )
                if not settled:
                    raise EventOutboxError("conflict")
                return EventOutboxReconciliation(outcome="published", publication=self._one_status(settled))

            return await self._using(check, read)

        return await _operation(timeout, work)


def _retry_guidance(response: _HTTPResult) -> tuple[int, str | None]:
    if response.status not in (429, 503):
        return 0, None
    raw = (response.retry_after or "").strip()
    if 0 < len(raw) <= 256:
        if raw.isascii() and raw.isdecimal():
            return min(int(raw), 604800) * 1000, None
        try:
            instant = parsedate_to_datetime(raw).astimezone(UTC)
            return 0, instant.isoformat()
        except (ValueError, TypeError, OverflowError):
            pass
    seconds = response.value.get("retry_after_seconds") if isinstance(response.value, dict) else None
    return (min(seconds, 604800) * 1000, None) if type(seconds) is int and seconds >= 0 else (0, None)
