"""Receive-only retained channel recovery over authenticated HTTPS SSE."""

from __future__ import annotations

import base64
import codecs
import json
import ssl
from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import dataclass, field
from datetime import datetime
from urllib.parse import urlsplit

import httpx

from .._sse import SseEvent, _parse_frame
from .realtime_catch_up import _sequence
from .realtime_expiry import RealtimeMessageExpiry, _notify_expiry
from .realtime_filters import RealtimeEventFilter
from .realtime_recovery import (
    RealtimeRecoveryError,
    RealtimeRecoveryEvent,
    RealtimeRecoveryRetry,
    RealtimeRecoveryTarget,
)
from .realtime_subscriptions import _identity


def _position(value: object) -> int:
    if not isinstance(value, str) or not value.isascii() or not value.isdecimal() or len(value) > 19:
        raise RealtimeRecoveryError("invalid_sse_sequence")
    result = int(value)
    _sequence(result)
    return result


@dataclass(frozen=True, kw_only=True)
class RealtimeSSEMessage:
    data: bytes = field(repr=False)
    binary: bool
    version: int
    event: str
    deleted: bool
    target_message_id: str | None = None
    expires_at: str | None = None
    metadata: dict[str, str] = field(default_factory=dict)
    message_id: None = None


class RealtimeSSEClient:
    """Use connect with RealtimeRecoveryCoordinator; no durable ACKs or inboxes."""

    def __init__(
        self,
        *,
        url: str,
        token: Callable[[], Awaitable[str]],
        event_filter: RealtimeEventFilter | None = None,
        on_message_expired: Callable[[RealtimeMessageExpiry], None] | None = None,
    ):
        parsed = urlsplit(url)
        if (
            parsed.scheme != "https"
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.query
            or parsed.fragment
        ):
            raise ValueError("An HTTPS URL without credentials, query or fragment is required")
        if not callable(token) or event_filter is not None and not isinstance(event_filter, RealtimeEventFilter):
            raise ValueError("Invalid SSE configuration")
        if on_message_expired is not None and not callable(on_message_expired):
            raise ValueError("Expiry observer must be callable")
        self._on_message_expired = on_message_expired
        self._url, self._token, self._filter = url, token, event_filter

    async def connect(self) -> RealtimeSSESession:
        return RealtimeSSESession(self)


class RealtimeSSESession:
    def __init__(self, client: RealtimeSSEClient):
        self._config = client
        self._http: httpx.AsyncClient | None = None
        self._response: httpx.Response | None = None
        self._frames: AsyncIterator[SseEvent] | None = None
        self._channel: str | None = None
        self._received = 0
        self._closed = False

    async def _open(self, after: int) -> RealtimeRecoveryTarget:
        await self._close_stream()
        try:
            token = await self._config._token()
        except Exception:
            raise RealtimeRecoveryError("token_provider_failed") from None
        if not isinstance(token, str) or not token or any(ord(c) < 33 or ord(c) > 126 for c in token):
            raise RealtimeRecoveryError("invalid_token")
        self._http = httpx.AsyncClient(trust_env=False, follow_redirects=False, timeout=httpx.Timeout(45, connect=10))
        request = self._http.build_request(
            "GET",
            self._config._url,
            params={"transport": "sse", "channel": self._channel},
            headers={"Authorization": "Bearer " + token, "Accept": "text/event-stream", "Last-Event-ID": str(after)},
        )
        try:
            self._response = await self._http.send(request, stream=True)
            status = self._response.status_code
            if status == 429 or status >= 500:
                raise RealtimeRecoveryRetry()
            if status != 200:
                raise RealtimeRecoveryError("sse_handshake_rejected")
            if self._response.headers.get("content-type", "").split(";", 1)[0].strip() != "text/event-stream":
                raise RealtimeRecoveryError("invalid_sse_content_type")
            self._frames = self._iterate_frames()
            frame = await anext(self._frames)
            data = self._data(frame)
            if (
                frame.event != "subscribed"
                or data.get("channel") != self._channel
                or _position(data.get("sequence")) != after
            ):
                raise RealtimeRecoveryError("invalid_sse_subscription")
            oldest, latest = _position(data.get("oldest_sequence")), _position(data.get("latest_sequence"))
            if oldest > latest and latest != 0:
                raise RealtimeRecoveryError("invalid_sse_bounds")
            self._received = after
            selected = self._config._filter
            return RealtimeRecoveryTarget(
                oldest_sequence=oldest,
                latest_sequence=latest,
                resync_required=after > latest or after < oldest - 1,
                filter_fingerprint=selected.fingerprint if selected and selected.values else None,
            )
        except httpx.TransportError as exc:
            await self._close_stream()
            self._transport_error(exc)
        except BaseException:
            await self._close_stream()
            raise

    async def subscribe(self, *, channel: str | None, inbox: bool, after: int) -> RealtimeRecoveryTarget:
        _identity(channel, inbox)
        _sequence(after)
        if self._closed or inbox or channel is None or self._channel is not None:
            raise RealtimeRecoveryError("unsupported_sse_subscription")
        self._channel = channel
        return await self._open(after)

    async def _iterate_frames(self) -> AsyncIterator[SseEvent]:
        assert self._response is not None
        decoder = codecs.getincrementaldecoder("utf-8")()
        buffer = ""
        try:
            async for chunk in self._response.aiter_bytes(chunk_size=4096):
                buffer = (buffer + decoder.decode(chunk)).replace("\r\n", "\n")
                if len(buffer.encode()) > 65536:
                    raise RealtimeRecoveryError("sse_frame_too_large")
                while "\n\n" in buffer:
                    frame, buffer = _parse_frame(buffer)
                    if frame is not None:
                        yield frame
            decoder.decode(b"", final=True)
        except httpx.TransportError as exc:
            self._transport_error(exc)
        except UnicodeError:
            raise RealtimeRecoveryError("invalid_sse_encoding") from None
        raise RealtimeRecoveryRetry()

    @staticmethod
    def _transport_error(exc: httpx.TransportError) -> None:
        cause: BaseException | None = exc
        while cause is not None:
            if isinstance(cause, ssl.SSLCertVerificationError):
                raise RealtimeRecoveryError("sse_tls_rejected") from None
            cause = cause.__cause__
        raise RealtimeRecoveryRetry() from None

    @staticmethod
    def _data(frame: SseEvent) -> dict:
        try:
            data = json.loads(frame.data)
        except (ValueError, TypeError):
            raise RealtimeRecoveryError("invalid_sse_frame") from None
        if not isinstance(data, dict):
            raise RealtimeRecoveryError("invalid_sse_frame")
        return data

    async def _next(self) -> RealtimeRecoveryEvent:
        if self._closed or self._frames is None:
            raise RealtimeRecoveryError("sse_session_closed")
        async for frame in self._frames:
            data = self._data(frame)
            if frame.event == "heartbeat" and data.get("channel") == self._channel:
                continue
            if frame.event == "resync_required":
                raise RealtimeRecoveryRetry()
            if (
                frame.event == "checkpoint"
                and data.get("code") == "message_expired"
                and data.get("channel") == self._channel
            ):
                sequence = _position(data.get("sequence"))
                if sequence != self._received + 1 or frame.id != str(sequence):
                    raise RealtimeRecoveryRetry()
                _notify_expiry(
                    self._config._on_message_expired,
                    RealtimeMessageExpiry(channel=self._channel, sequence=sequence, transport="sse"),
                )
                self._received = sequence
                return RealtimeRecoveryEvent(sequence=sequence, data=None, checkpoint=True)
            if frame.event != "message" or data.get("channel") != self._channel:
                raise RealtimeRecoveryError("sse_stream_rejected")
            sequence = _position(data.get("sequence"))
            if sequence != self._received + 1 or frame.id != str(sequence):
                raise RealtimeRecoveryRetry()
            try:
                encoded = data.get("data_base64")
                payload = base64.b64decode(encoded, validate=True) if isinstance(encoded, str) else None
                metadata = data.get("metadata") or {}
                RealtimeEventFilter(metadata)
            except (ValueError, TypeError):
                raise RealtimeRecoveryError("invalid_sse_message") from None
            version, event = data.get("version", 1), data.get("message_event", "created")
            binary, deleted, target = data.get("binary"), data.get("deleted", False), data.get("target_message_id")
            if (
                payload is None
                or len(payload) > 4096
                or type(binary) is not bool
                or type(deleted) is not bool
                or type(version) is not int
                or not 1 <= version <= 2**63 - 1
                or event not in ("created", "updated", "deleted")
                or target is not None
                and (not isinstance(target, str) or len(target) > 256)
            ):
                raise RealtimeRecoveryError("invalid_sse_message")
            expiry = data.get("expires_at")
            if expiry is not None:
                try:
                    if (
                        not isinstance(expiry, str)
                        or len(expiry) > 64
                        or datetime.fromisoformat(expiry).utcoffset() is None
                    ):
                        raise ValueError()
                except ValueError:
                    raise RealtimeRecoveryError("invalid_message_expiry") from None
            self._received = sequence
            selected = self._config._filter
            skipped = selected is not None and any(metadata.get(k) != v for k, v in selected.values.items())
            message = (
                None
                if skipped
                else RealtimeSSEMessage(
                    expires_at=expiry,
                    data=payload,
                    binary=binary,
                    version=version,
                    event="deleted" if deleted else event,
                    deleted=deleted or event == "deleted",
                    target_message_id=target,
                    metadata=dict(metadata),
                )
            )
            return RealtimeRecoveryEvent(sequence=sequence, data=message, checkpoint=skipped)
        raise RealtimeRecoveryRetry()

    async def replay(self, *, after: int, through: int) -> AsyncIterator[RealtimeRecoveryEvent]:
        _sequence(after)
        _sequence(through)
        if after != self._received:
            target = await self._open(after)
            if target.resync_required:
                raise RealtimeRecoveryRetry()
        while self._received < through:
            yield await self._next()

    async def events(self) -> AsyncIterator[RealtimeRecoveryEvent]:
        while not self._closed:
            yield await self._next()

    async def _close_stream(self) -> None:
        response, client = self._response, self._http
        self._response = self._http = self._frames = None
        try:
            if response is not None:
                await response.aclose()
        finally:
            if client is not None:
                await client.aclose()

    async def aclose(self) -> None:
        self._closed = True
        await self._close_stream()
