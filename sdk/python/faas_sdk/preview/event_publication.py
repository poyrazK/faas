"""One-shot native publication and bounded reads of untrusted response bodies."""

from __future__ import annotations

import asyncio
import json
import math
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, TypeVar

import httpx

from ..job_runtime import JobError, _scope
from .event_outbox_state import EventOutboxError, EventPublicationOutcome, _receipt
from .pipeline_state import PipelineScope

T = TypeVar("T")


async def _operation(timeout: float, work: Callable[[Callable[[], None], Callable[[], float]], Awaitable[T]]) -> T:
    if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 2147483.647:
        raise EventOutboxError("invalid_request")

    async def run(check: Callable[[], None]) -> T:
        deadline = asyncio.get_running_loop().time() + timeout

        def remaining() -> float:
            check()
            seconds = deadline - asyncio.get_running_loop().time()
            if seconds < 0.001:
                raise EventOutboxError("deadline")
            return seconds

        remaining()
        result = await work(check, remaining)
        check()
        return result

    try:
        return await _scope(timeout, run)
    except JobError as error:
        raise EventOutboxError("deadline" if error.code == "deadline" else "invalid_request") from None


@dataclass(frozen=True, kw_only=True)
class _HTTPResult:
    status: int
    value: Any
    retry_after: str | None = None


class _EventTransport:
    def __init__(self, scope: PipelineScope, token: str, httpx_args: dict[str, Any] | None = None):
        if not isinstance(token, str) or not token or any(c in token for c in "\r\n\0"):
            raise EventOutboxError("invalid_request")
        self.scope, self.token = scope, token
        self.args = dict(httpx_args or {})
        if set(self.args) & {"base_url", "headers", "auth", "params", "cookies", "follow_redirects"}:
            raise EventOutboxError("invalid_request")
        self.args.setdefault("timeout", None)

    async def send(self, slug: str, body: str, *, verify: bool = False, timeout: float) -> _HTTPResult:
        from urllib.parse import quote

        async def run(check: Callable[[], None], remaining: Callable[[], float]) -> _HTTPResult:
            try:
                path = (
                    "/v1/apps/" + quote(slug, safe="") + ("/events/verify-publication" if verify else "/events:publish")
                )
                async with httpx.AsyncClient(
                    base_url=self.scope.base_url,
                    headers={
                        "authorization": "Bearer " + self.token,
                        "content-type": "application/json",
                        "accept": "application/json",
                    },
                    follow_redirects=False,
                    **self.args,
                ) as client:
                    async with client.stream("POST", path, content=body.encode("utf-8")) as response:
                        data = bytearray()
                        async for chunk in response.aiter_bytes(chunk_size=8192):
                            check()
                            if len(data) + len(chunk) > 1048576:
                                raise ValueError
                            data.extend(chunk)
                        check()
                        try:
                            value = json.loads(data)
                        except (ValueError, UnicodeError):
                            value = None
                        return _HTTPResult(
                            status=response.status_code, value=value, retry_after=response.headers.get("retry-after")
                        )
            except Exception:
                check()
                return _HTTPResult(status=0, value=None)

        return await _operation(timeout, run)


def _publication_outcome(response: _HTTPResult, scope: PipelineScope, key: str) -> EventPublicationOutcome:
    if 200 <= response.status < 300:
        try:
            return EventPublicationOutcome(state="published", receipt=_receipt(response.value, scope, key))
        except EventOutboxError:
            pass
    if 400 <= response.status < 500 and response.status not in (408, 429):
        return EventPublicationOutcome(state="blocked", code="publication_rejected", http_status=response.status)
    return EventPublicationOutcome(state="unconfirmed", code="unconfirmed", http_status=response.status or None)
