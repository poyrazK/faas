"""Bounded raw JSON callbacks through the configured SDK HTTPX client."""

from __future__ import annotations

import asyncio
import json
import re
import time
from email.utils import format_datetime, parsedate_to_datetime
from typing import Any

import httpx

from ._event_acknowledgment_protocol import (
    Budget,
    CallbackFailure,
    Control,
    DeliveryFence,
    EventAcknowledgmentWorkerError,
    EventAcknowledgmentWorkerResult,
    callback_error,
    observation,
    pause,
)
from ._rfc7807 import FaasProblemError

_RESPONSE_MAX = 65536


def _constant(_value: str) -> Any:
    raise ValueError("invalid acknowledgment JSON")


def _retry_delay(status: int, headers: httpx.Headers, body: bytes, budget: Budget) -> float | None:
    if status not in (408, 425, 429) and not 500 <= status <= 599:
        return None
    delay: float = budget.backoff_ms
    try:
        problem = json.loads(body, parse_constant=_constant)
    except (ValueError, UnicodeError):
        problem = None
    seconds = problem.get("retry_after_seconds") if isinstance(problem, dict) else None
    if seconds is not None:
        if type(seconds) is not int or seconds < 0 or seconds > budget.interval_ms / 1000:
            return None
        delay = max(delay, seconds * 1000)
    else:
        hint = headers.get("Retry-After")
        if hint is None and isinstance(problem, dict):
            hint = problem.get("retry_after")
        if hint is not None:
            if not isinstance(hint, str) or not hint.strip() or len(hint) > 128:
                return None
            hint = hint.strip()
            if re.fullmatch(r"[0-9]{1,10}", hint):
                delay = max(delay, int(hint) * 1000)
            else:
                try:
                    at = parsedate_to_datetime(hint)
                    if format_datetime(at, usegmt=True) != hint:
                        return None
                    delay = max(delay, (at.timestamp() - time.time()) * 1000)
                except (ValueError, OverflowError, TypeError):
                    return None
    return delay if delay <= budget.interval_ms else None


async def _request(
    client: httpx.AsyncClient, path: str, token: str, action: str, value: bytes | str | None, budget: Budget
) -> Any:
    body: bytes | None = None
    if action != "get":
        body = b'{"token":"' + token.encode("ascii") + b'"'
        if action == "complete":
            # The receipt helper validated these bytes. Never parse/re-serialize saved JSON.
            body += b',"result":' + value
        elif action in ("retry", "dead-letter"):
            body += b',"reason":' + json.dumps(value).encode("ascii")
        body += b"}"
    failure: CallbackFailure | None = None
    result: Any = None
    try:
        async with asyncio.timeout(budget.timeout_ms / 1000):
            async with client.stream(
                "GET" if action == "get" else "POST",
                path if action == "get" else path + "/" + action,
                content=body,
                headers={"Content-Type": "application/json"} if body is not None else {},
                timeout=budget.timeout_ms / 1000,
                follow_redirects=False,
            ) as response:
                if type(response.status_code) is not int or not 100 <= response.status_code <= 599:
                    raise CallbackFailure()
                raw = bytearray()
                async for chunk in response.aiter_bytes(chunk_size=_RESPONSE_MAX):
                    if len(raw) + len(chunk) > _RESPONSE_MAX:
                        raise CallbackFailure(response.status_code)
                    raw.extend(chunk)
                if response.status_code != 200:
                    failure = CallbackFailure(
                        response.status_code, _retry_delay(response.status_code, response.headers, bytes(raw), budget)
                    )
                else:
                    result = json.loads(raw, parse_constant=_constant)
    except asyncio.CancelledError:
        raise
    except CallbackFailure as error:
        failure = error
    except (TimeoutError, httpx.TransportError):
        failure = CallbackFailure(delay_ms=budget.backoff_ms)
    except FaasProblemError as error:
        # The wrapper removes Retry-After extensions. Do not invent a shorter retry.
        status = error.status if type(error.status) is int and 100 <= error.status <= 599 else None
        failure = CallbackFailure(status)
    except Exception:
        failure = CallbackFailure()
    # Outside exception handlers: never attach an HTTP exception/request as __context__.
    if failure is not None:
        raise failure
    return result


async def call(
    client: httpx.AsyncClient,
    path: str,
    token: str,
    action: str,
    budget: Budget,
    control: Control,
    value: bytes | str | None = None,
) -> Any:
    control.check()
    pending = asyncio.create_task(_request(client, path, token, action, value, budget))
    stopped = asyncio.create_task(control.event.wait())
    try:
        await asyncio.wait((pending, stopped), return_when=asyncio.FIRST_COMPLETED)
        if pending.done():
            return pending.result()
        control.check()
    finally:
        # Only HTTP/wait tasks are cancelled here, never business work or SQL.
        for task in (pending, stopped):
            if not task.done():
                task.cancel()
        await asyncio.gather(pending, stopped, return_exceptions=True)


async def terminal(
    client: httpx.AsyncClient,
    path: str,
    token: str,
    delivery: DeliveryFence,
    budget: Budget,
    action: str,
    value: bytes | str,
    control: Control,
) -> EventAcknowledgmentWorkerResult:
    """Same fenced terminal callback for live workers and saved-intent recovery."""
    for attempt in range(budget.attempts):
        try:
            status = observation(delivery, await call(client, path, token, action, budget, control, value))
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
            if error.delay_ms is None or attempt + 1 == budget.attempts:
                raise callback_error(error, control.result) from None
            await pause(error.delay_ms, control)
    raise EventAcknowledgmentWorkerError("event_acknowledgment_unconfirmed", control.result)
