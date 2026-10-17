"""Claim and deliver saved callbacks only. The application owns polling and host lifetime."""

from __future__ import annotations

import asyncio
import math
import re
import secrets
import time
from dataclasses import dataclass
from typing import Any

import httpx

from ._event_acknowledgment_callbacks import terminal
from ._event_acknowledgment_protocol import (
    Budget,
    Control,
    EventAcknowledgmentWorkerError,
    EventAcknowledgmentWorkerOptions,
    owned_operation,
    settings,
)
from .client import AuthenticatedClient
from .event_acknowledgment_intents import (
    _COLUMNS,
    _KEY,
    _TABLE,
    EventAcknowledgmentIntentError,
    EventAcknowledgmentIntentStatus,
    _bytes,
    _CallbackOptionsError,
    _hash,
    _integer,
    _query,
    _status,
    _store_error,
    _transaction,
)
from .event_consumer_transactions import _result, _uuid

_SUPPORTED = object()


@dataclass(frozen=True, repr=False)
class _Saved:
    account_id: str
    app_id: str
    invocation_id: str
    replay_generation: int
    attempt: int
    claim: bytes
    identity: bytes
    token: str
    slug: str
    seconds: int
    action: str
    reason: str
    body: bytes | None

    def __repr__(self) -> str:
        return "SavedAcknowledgmentIntent{redacted}"

    def key(self) -> tuple[Any, ...]:
        return (self.account_id, self.app_id, self.invocation_id, self.replay_generation, self.attempt, self.claim)


def _saved(row: Any, account: str, app: str, claim: bytes) -> _Saved:
    status = _status(row)
    if len(row) != 20:
        raise _store_error()
    token, slug, seconds, body, identity, request, action_digest, capability_digest = row[12:]
    if (
        status.state not in ("pending", "unconfirmed")
        or not isinstance(token, str)
        or re.fullmatch(r"[a-f0-9]{64}", token) is None
        or not isinstance(slug, str)
        or re.fullmatch(r"[a-z0-9][a-z0-9-]{0,252}", slug) is None
    ):
        raise _store_error()
    seconds = _integer(seconds, 1, 300)
    identity = _bytes(identity)
    _bytes(request)
    result: bytes | None = None
    invalid = False
    if status.action == "complete":
        if not isinstance(body, str):
            raise _store_error()
        try:
            result = _result(body)
        except Exception:
            invalid = True
    elif body is not None:
        invalid = True
    if invalid:
        raise _store_error()
    if _bytes(capability_digest) != _hash(token) or _bytes(action_digest) != _hash(
        status.action + "\n" + status.reason + "\n" + (body or "")
    ):
        raise _store_error()
    return _Saved(
        account,
        app,
        status.invocation_id,
        status.replay_generation,
        status.attempt,
        claim,
        identity,
        token,
        slug,
        seconds,
        status.action,
        status.reason,
        result,
    )


def _duration(value: int) -> int:
    if type(value) is not int or not 1000 <= value <= 300000:
        raise ValueError("invalid acknowledgment intent claim duration")
    return value


def _backoff(value: int) -> int:
    if type(value) is not int or not 1 <= value <= 3600000:
        raise ValueError("invalid acknowledgment intent backoff")
    return value


def _client(client: AuthenticatedClient) -> httpx.AsyncClient:
    if not isinstance(client, AuthenticatedClient):
        raise ValueError("authenticated SDK client required")
    return client.get_async_httpx_client()


class EventAcknowledgmentIntentClaim:
    """Process-local capability, bound to one loop. Never serialize or persist this object."""

    __slots__ = ("_saved", "_busy", "_loop")

    def __init__(self, key: object, saved: _Saved):
        if key is not _SUPPORTED:
            raise TypeError("use aclaim_event_acknowledgment_intent")
        self._saved, self._busy, self._loop = saved, False, asyncio.get_running_loop()

    def __repr__(self) -> str:
        return "EventAcknowledgmentIntentClaim{redacted}"

    __str__ = __repr__

    def __reduce_ex__(self, _protocol: int) -> Any:
        raise TypeError("recovery claims cannot be persisted")

    def __copy__(self) -> Any:
        raise TypeError("recovery claims cannot be copied")

    def __deepcopy__(self, _memo: Any) -> Any:
        raise TypeError("recovery claims cannot be copied")

    async def deliver(
        self,
        client: AuthenticatedClient,
        connection: Any,
        *,
        backoff_ms: int = 1000,
        callback_options: EventAcknowledgmentWorkerOptions | None = None,
        stop_event: asyncio.Event | None = None,
    ) -> EventAcknowledgmentIntentStatus:
        """Send the saved callback and return committed local status, including uncertainty."""
        http = _client(client)
        _backoff(backoff_ms)
        budget = settings(callback_options, self._saved.seconds)
        if self._loop is not asyncio.get_running_loop():
            raise ValueError("recovery claim belongs to another event loop")
        if self._busy:
            raise EventAcknowledgmentWorkerError("event_acknowledgment_worker_busy")
        self._busy = True
        try:
            return await owned_operation(
                lambda control: self._deliver(http, connection, budget, backoff_ms, control), stop_event
            )
        finally:
            self._busy = False

    async def _deliver(
        self, http: httpx.AsyncClient, connection: Any, budget: Budget, backoff_ms: int, control: Control
    ) -> EventAcknowledgmentIntentStatus:
        saved = self._saved
        key = saved.key()
        started = time.monotonic()

        async def preflight(cursor: Any) -> tuple[float, bool]:
            row = await _query(
                cursor,
                f"SELECT extract(epoch FROM claim_expires_at-clock_timestamp())::double precision FROM {_TABLE} WHERE {_KEY} AND replay_generation=%s::bigint AND attempt=%s AND state IN ('pending','unconfirmed') AND claim_token=%s AND claim_expires_at>clock_timestamp()",
                key,
                control,
            )
            if row is None:
                raise EventAcknowledgmentIntentError("event_acknowledgment_intent_claim_lost")
            remaining = row[0]
            if type(remaining) is not float or not math.isfinite(remaining) or not 0 < remaining <= 300:
                raise EventAcknowledgmentIntentError("event_acknowledgment_intent_claim_lost")
            committed = False
            if saved.action != "complete":
                receipt = await _query(
                    cursor,
                    "SELECT EXISTS(SELECT 1 FROM public.gregale_event_consumer_inbox WHERE identity_digest=%s AND account_id=%s::uuid AND app_id=%s::uuid)",
                    (saved.identity, saved.account_id, saved.app_id),
                    control,
                )
                if receipt is None or type(receipt[0]) is not bool:
                    raise _store_error()
                committed = receipt[0]
            return remaining, committed

        remaining, committed = await _transaction(connection, preflight, control)
        # Conservatively subtract all elapsed time, including both DB round trips/COMMIT.
        remaining -= time.monotonic() - started
        if remaining <= 0:
            raise EventAcknowledgmentIntentError("event_acknowledgment_intent_claim_lost")
        control.check()
        callback_control = Control(control.event)

        async def forward() -> None:
            await control.event.wait()
            callback_control.stop()

        watcher = asyncio.create_task(forward())
        timer = asyncio.get_running_loop().call_later(remaining, callback_control.stop)
        code, state = "unconfirmed", "unconfirmed"
        try:
            if committed:
                state, code = "superseded", "receipt_committed"
            else:
                try:
                    await terminal(
                        http,
                        f"/v1/apps/{saved.slug}/event-deliveries/{saved.invocation_id}/acknowledgment",
                        saved.token,
                        saved,
                        budget,
                        saved.action,
                        saved.body if saved.action == "complete" else saved.reason,
                        callback_control,
                    )
                except EventAcknowledgmentWorkerError as error:
                    if error.code == "event_acknowledgment_lease_lost":
                        state, code = "superseded", "lease_lost"
                except asyncio.CancelledError:
                    pass  # Callback deadline or owner stop; settlement still needs the live DB claim.
                except Exception:
                    pass  # Retain only fixed local uncertainty, never an arbitrary callback cause.
                else:
                    state, code = "confirmed", "callback_confirmed"
        finally:
            timer.cancel()
            watcher.cancel()
            await asyncio.gather(watcher, return_exceptions=True)

        async def settle(cursor: Any) -> EventAcknowledgmentIntentStatus:
            row = await _query(
                cursor,
                f"UPDATE {_TABLE} SET state=%s,last_code=%s,token=CASE WHEN %s IN ('confirmed','superseded') THEN NULL ELSE token END,result_body=CASE WHEN %s IN ('confirmed','superseded') THEN NULL ELSE result_body END,claim_token=NULL,claim_expires_at=NULL,next_attempt_at=CASE WHEN %s='unconfirmed' THEN clock_timestamp()+%s::bigint*interval '1 millisecond' ELSE next_attempt_at END,updated_at=clock_timestamp() WHERE {_KEY} AND replay_generation=%s::bigint AND attempt=%s AND state IN ('pending','unconfirmed') AND claim_token=%s AND claim_expires_at>clock_timestamp() RETURNING {_COLUMNS}",
                (state, code, state, state, state, backoff_ms, *key),
                control,
            )
            if row is None:
                raise EventAcknowledgmentIntentError("event_acknowledgment_intent_claim_lost")
            return _status(row)

        return await _transaction(connection, settle, control)


async def _claim(
    connection: Any,
    account: str,
    app: str,
    duration: int,
    callback_options: EventAcknowledgmentWorkerOptions | None,
    control: Control,
) -> EventAcknowledgmentIntentClaim:
    claim = secrets.token_bytes(32)

    async def acquire(cursor: Any) -> _Saved:
        row = await _query(
            cursor,
            f"SELECT {_COLUMNS},token,app_slug,lease_seconds,result_body,identity_digest,request_digest,action_digest,capability_digest FROM {_TABLE} WHERE account_id=%s::uuid AND app_id=%s::uuid AND state IN ('pending','unconfirmed') AND next_attempt_at<=clock_timestamp() AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) ORDER BY next_attempt_at,created_at,invocation_id,replay_generation,attempt LIMIT 1 FOR UPDATE SKIP LOCKED",
            (account, app),
            control,
        )
        if row is None:
            raise EventAcknowledgmentIntentError("event_acknowledgment_intent_empty")
        saved = _saved(row, account, app, claim)
        invalid = False
        try:
            settings(callback_options, saved.seconds)
        except ValueError:
            invalid = True
        if invalid:
            raise _CallbackOptionsError()
        await _query(
            cursor,
            f"UPDATE {_TABLE} SET claim_token=%s,claim_expires_at=clock_timestamp()+%s::bigint*interval '1 millisecond',delivery_attempts=least(delivery_attempts+1,2147483647),updated_at=clock_timestamp() WHERE {_KEY} AND replay_generation=%s::bigint AND attempt=%s",
            (claim, duration, account, app, saved.invocation_id, saved.replay_generation, saved.attempt),
            control,
            fetch="none",
        )
        return saved

    saved = await _transaction(connection, acquire, control)
    return EventAcknowledgmentIntentClaim(_SUPPORTED, saved)


async def aclaim_event_acknowledgment_intent(
    connection: Any,
    account_id: str,
    app_id: str,
    *,
    claim_duration_ms: int = 30000,
    callback_options: EventAcknowledgmentWorkerOptions | None = None,
    stop_event: asyncio.Event | None = None,
) -> EventAcknowledgmentIntentClaim:
    """Claim one due row using DB time/SKIP LOCKED. Does not send a callback."""
    account, app, duration = _uuid(account_id), _uuid(app_id), _duration(claim_duration_ms)
    return await owned_operation(
        lambda control: _claim(connection, account, app, duration, callback_options, control), stop_event
    )


async def arecover_next_event_acknowledgment_intent(
    client: AuthenticatedClient,
    connection: Any,
    account_id: str,
    app_id: str,
    *,
    claim_duration_ms: int = 30000,
    backoff_ms: int = 1000,
    callback_options: EventAcknowledgmentWorkerOptions | None = None,
    stop_event: asyncio.Event | None = None,
) -> EventAcknowledgmentIntentStatus:
    """One bounded claim/delivery iteration; never renews a platform lease or runs business code."""
    http = _client(client)
    account, app = _uuid(account_id), _uuid(app_id)
    duration, delay = _duration(claim_duration_ms), _backoff(backoff_ms)

    async def recover(control: Control) -> EventAcknowledgmentIntentStatus:
        claim = await _claim(connection, account, app, duration, callback_options, control)
        return await claim._deliver(http, connection, settings(callback_options, claim._saved.seconds), delay, control)

    return await owned_operation(recover, stop_event)
