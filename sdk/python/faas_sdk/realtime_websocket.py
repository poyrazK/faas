"""Native managed realtime v2 transport with bounded retained-event buffering."""

from __future__ import annotations

import asyncio
import base64
import json
import math
import ssl
from collections import deque
from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any
from urllib.parse import urlsplit

from .realtime_backpressure import RealtimeBackpressureMonitor
from .realtime_catch_up import _sequence
from .realtime_direct_messages import RealtimeWebSocketDirectMessages
from .realtime_drain import _RecoveryDrained
from .realtime_expiry import RealtimeMessageExpiry, _notify_expiry
from .realtime_filters import RealtimeEventFilter
from .realtime_recovery import (
    RealtimeRecoveryError,
    RealtimeRecoveryEvent,
    RealtimeRecoveryRetry,
    RealtimeRecoveryTarget,
)
from .realtime_subscriptions import _identity
from .realtime_websocket_presence import _REJECTIONS, RealtimeWebSocketPresence
from .realtime_websocket_reads import RealtimeWebSocketReadProgress


def _durable_subscription(value: str | None) -> None:
    if value is not None and (
        not isinstance(value, str)
        or not value
        or len(value.encode()) > 128
        or value.strip() != value
        or any(c in value for c in "\0\r\n")
    ):
        raise ValueError("Subscription name must be nonempty, trimmed and within 128 UTF-8 bytes")


@dataclass(frozen=True, kw_only=True)
class RealtimeWebSocketMessage:
    message_id: str
    data: bytes = field(repr=False)
    binary: bool
    version: int
    event: str
    deleted: bool
    target_message_id: str | None = None
    expires_at: str | None = None
    metadata: dict[str, str] = field(default_factory=dict)


class RealtimeWebSocketClient:
    """Connection factory; pass client.connect to RealtimeRecoveryCoordinator.

    Bearer tokens are resolved anew per connection and sent only in HTTP headers.
    One returned session supports one channel or inbox consumer. No read markers
    are advanced: processing ACKs and user read progress are separate concepts.
    """

    def __init__(
        self,
        *,
        url: str,
        token: Callable[[], Awaitable[str]],
        consumer: str | None = None,
        durable_subscription: str | None = None,
        event_filter: RealtimeEventFilter | None = None,
        checkpoint_timeout_seconds: float = 10,
        on_message_expired: Callable[[RealtimeMessageExpiry], None] | None = None,
        backpressure: RealtimeBackpressureMonitor | None = None,
        presence: RealtimeWebSocketPresence | None = None,
        read_progress: RealtimeWebSocketReadProgress | None = None,
        direct_messages: RealtimeWebSocketDirectMessages | None = None,
        buffer_size: int = 256,
        ping_interval_seconds: float = 20,
        ping_timeout_seconds: float = 20,
        open_timeout_seconds: float = 10,
    ):
        parsed = urlsplit(url)
        if (
            parsed.scheme != "wss"
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.fragment
            or parsed.query
        ):
            raise ValueError("A wss URL without credentials, query or fragment is required")
        if not callable(token):
            raise ValueError("An async bearer token provider is required")
        if consumer is not None and (
            not isinstance(consumer, str)
            or not 1 <= len(consumer) <= 64
            or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:-" for c in consumer)
        ):
            raise ValueError("Invalid inbox consumer")
        if type(buffer_size) is not int or not 1 <= buffer_size <= 8192:
            raise ValueError("buffer_size must be between 1 and 8192")
        for duration in (ping_interval_seconds, ping_timeout_seconds, open_timeout_seconds, checkpoint_timeout_seconds):
            if type(duration) not in (int, float) or not math.isfinite(duration) or not 0.1 <= duration <= 300:
                raise ValueError("WebSocket timeouts must be between 0.1 and 300 seconds")
        if presence is not None and not isinstance(presence, RealtimeWebSocketPresence):
            raise ValueError("Invalid presence binding")
        _durable_subscription(durable_subscription)
        if event_filter is not None and not isinstance(event_filter, RealtimeEventFilter):
            raise ValueError("Invalid event filter")
        if event_filter is not None and consumer is not None:
            raise ValueError("Event filters require a channel")
        if backpressure is not None and not isinstance(backpressure, RealtimeBackpressureMonitor):
            raise ValueError("Invalid backpressure monitor")
        self._backpressure = backpressure
        if on_message_expired is not None and not callable(on_message_expired):
            raise ValueError("Expiry observer must be callable")
        self._on_message_expired = on_message_expired
        self._event_filter = event_filter
        if durable_subscription is not None and consumer is not None:
            raise ValueError("Choose a durable channel subscription or an inbox consumer")
        self._durable_subscription = (
            event_filter.scoped_subscription(durable_subscription)
            if event_filter is not None and durable_subscription is not None
            else durable_subscription
        )
        self._checkpoint_timeout = checkpoint_timeout_seconds
        if read_progress is not None and not isinstance(read_progress, RealtimeWebSocketReadProgress):
            raise ValueError("Invalid read-progress binding")
        if direct_messages is not None and not isinstance(direct_messages, RealtimeWebSocketDirectMessages):
            raise ValueError("Invalid direct-message handler")
        self._direct_messages = direct_messages
        self._read_progress = read_progress
        self._presence = presence
        self._url, self._token, self._consumer = url, token, consumer
        self._buffer = buffer_size
        self._ping, self._pong, self._open = ping_interval_seconds, ping_timeout_seconds, open_timeout_seconds

    async def connect(self) -> RealtimeWebSocketSession:
        """Open an authenticated v2 socket; optional dependency loaded lazily."""
        socket = await self._open_socket()
        return RealtimeWebSocketSession(
            socket,
            consumer=self._consumer,
            buffer_size=self._buffer,
            presence=self._presence,
            read_progress=self._read_progress,
            direct_messages=self._direct_messages,
            durable_subscription=self._durable_subscription,
            event_filter=self._event_filter,
            checkpoint_timeout_seconds=self._checkpoint_timeout,
            on_message_expired=self._on_message_expired,
            backpressure=self._backpressure,
        )

    async def _open_socket(self) -> Any:
        """Shared handshake implementation for dedicated and multiplexed sockets."""
        try:
            from websockets.asyncio.client import connect
            from websockets.exceptions import InvalidStatus
        except ImportError:
            raise RealtimeRecoveryError("websocket_dependency_missing") from None
        try:
            token = await self._token()
        except Exception:
            raise RealtimeRecoveryError("token_provider_failed") from None
        if not isinstance(token, str) or not token or any(ord(c) < 33 or ord(c) > 126 for c in token):
            raise RealtimeRecoveryError("invalid_token")

        class DirectConnect(connect):
            def process_redirect(self, exc):
                # Never forward bearer credentials to a redirected endpoint.
                return exc

        try:
            socket = await DirectConnect(
                self._url,
                additional_headers={"Authorization": f"Bearer {token}"},
                subprotocols=["gregale.realtime.v2"],
                ping_interval=self._ping,
                ping_timeout=self._pong,
                open_timeout=self._open,
                close_timeout=5,
                max_size=8192,
                max_queue=16,
                compression=None,
                proxy=None,
            )
        except InvalidStatus as exc:
            if exc.response.status_code in (408, 429, 500, 502, 503, 504):
                raise RealtimeRecoveryRetry() from None
            raise RealtimeRecoveryError("handshake_rejected") from None
        except ssl.SSLCertVerificationError:
            raise RealtimeRecoveryError("tls_rejected") from None
        except (OSError, TimeoutError):
            raise RealtimeRecoveryRetry() from None
        except Exception:
            raise RealtimeRecoveryError("handshake_failed") from None
        if socket.subprotocol != "gregale.realtime.v2":
            await socket.close()
            raise RealtimeRecoveryError("subprotocol_rejected")
        return socket


class RealtimeWebSocketSession:
    """Native recovery-session adapter; consume via the recovery coordinator.

    Iterator progress ACKs the preceding yielded event. Applications consuming
    manually must finish each event's effects before requesting the next event.
    Always aclose in finally, including cancellation and processing failures.
    """

    def __init__(
        self,
        socket: Any,
        *,
        consumer: str | None,
        buffer_size: int,
        presence: RealtimeWebSocketPresence | None = None,
        read_progress: RealtimeWebSocketReadProgress | None = None,
        direct_messages: RealtimeWebSocketDirectMessages | None = None,
        durable_subscription: str | None = None,
        event_filter: RealtimeEventFilter | None = None,
        checkpoint_timeout_seconds: float = 10,
        on_message_expired: Callable[[RealtimeMessageExpiry], None] | None = None,
        backpressure: RealtimeBackpressureMonitor | None = None,
    ):
        _durable_subscription(durable_subscription)
        if on_message_expired is not None and not callable(on_message_expired):
            raise ValueError("Expiry observer must be callable")
        self._on_message_expired = on_message_expired
        self._event_filter = event_filter
        self._durable_subscription = durable_subscription
        self._checkpoint_timeout = checkpoint_timeout_seconds
        self._confirmed_sequence: int | None = None
        self._on_checkpoint: Callable[[int], None] | None = None
        self._pending_ack: asyncio.Future[None] | None = None
        self._pending_sequence: int | None = None
        self._resume_sequence: int | None = None
        self._needs_resync = False
        self._socket, self._consumer = socket, consumer
        self._presence = presence
        self._read_progress = read_progress
        self._direct_messages = direct_messages
        self.backpressure = backpressure if backpressure is not None else RealtimeBackpressureMonitor()
        self._last_queue_time = 0.0
        self._queue: deque[RealtimeRecoveryEvent] = deque()
        self._buffer_size = buffer_size
        self._changed = asyncio.Event()
        self._failure: Exception | None = None
        self._reader: asyncio.Task[None] | None = None
        self._target: asyncio.Future[RealtimeRecoveryTarget] | None = None
        self._channel: str | None = None
        self._inbox = self._active = self._closed = False
        self._received = self._acked = 0
        self._iterating = False
        self._reset: asyncio.Future[None] | None = None
        self._reset_position: int | None = None
        self._drain_cutoff: int | None = None
        self._yielded_sequence: int | None = None
        self._sent_ack: int | None = None

    @property
    def confirmed_sequence(self) -> int | None:
        """Last server-confirmed durable checkpoint, distinct from local processing."""
        return self._confirmed_sequence

    def _confirm(self, sequence: int) -> None:
        self._confirmed_sequence = sequence
        if self._on_checkpoint is not None:
            self._on_checkpoint(sequence)

    async def _wait_control(self, future: asyncio.Future) -> None:
        while not future.done():
            self._changed.clear()
            if self._failure is not None:
                raise self._failure
            if self._closed:
                raise RealtimeRecoveryError("session_closed")
            if future.done():
                break
            await self._changed.wait()
        future.result()

    def begin_drain(self) -> None:
        """Freeze local retained-event admission; wire removal follows final ACK."""
        if self._inbox:
            raise RealtimeRecoveryError("drain_unsupported")
        if self._closed or not self._active or self._failure is not None:
            raise RealtimeRecoveryError("drain_not_ready")
        if self._drain_cutoff is None:
            self._drain_cutoff = self._received
            self._changed.set()

    def drain_progress(self, processed_sequence: int) -> tuple[int, bool, int | None]:
        unfinished = len(self._queue) + int(
            self._yielded_sequence is not None and self._yielded_sequence > processed_sequence
        )
        sent = self._sent_ack is not None and self._sent_ack >= processed_sequence
        return unfinished, sent, self._confirmed_sequence

    def _enqueue(self, event: RealtimeRecoveryEvent) -> None:
        if self._drain_cutoff is not None:
            return
        # Consecutive skips have no intervening effects; one ordered boundary
        # represents the entire range without letting sparse filters fill queues.
        if event.checkpoint and self._queue and self._queue[-1].checkpoint:
            self._queue[-1] = event
        elif len(self._queue) >= self._buffer_size:
            self.backpressure._overflow(self)
            raise RealtimeRecoveryRetry()
        else:
            self._queue.append(event)
            self.backpressure._append(self)

    async def _send(self, frame: dict) -> None:
        try:
            await self._socket.send(json.dumps(frame, ensure_ascii=False, separators=(",", ":"), allow_nan=False))
        except Exception:
            error = RealtimeRecoveryRetry()
            self._failure = error
            self.backpressure._invalidate(self)
            if self._presence is not None:
                self._presence._invalidate(self)
            if self._read_progress is not None:
                self._read_progress._invalidate(self)
            self._changed.set()
            raise error from None

    async def subscribe(self, *, channel: str | None, inbox: bool, after: int) -> RealtimeRecoveryTarget:
        _identity(channel, inbox)
        _sequence(after)
        if self._closed or self._active or self._target is not None and not self._target.done():
            raise RealtimeRecoveryError("invalid_session_state")
        if inbox and self._event_filter is not None:
            raise RealtimeRecoveryError("event_filter_requires_channel")
        if inbox and self._durable_subscription is not None:
            raise RealtimeRecoveryError("durable_subscription_requires_channel")
        if inbox and self._consumer is None:
            raise RealtimeRecoveryError("inbox_consumer_required")
        if self._reader is not None and (channel != self._channel or inbox != self._inbox):
            raise RealtimeRecoveryError("stream_identity_changed")
        self.backpressure._bind(self, self._buffer_size)
        self._channel, self._inbox, self._received, self._acked = channel, inbox, after, after
        if self._presence is not None:
            if inbox:
                raise RealtimeRecoveryError("presence_requires_channel")
            self._presence._bind(self, channel)
        if self._read_progress is not None:
            self._read_progress._bind(self, channel, inbox)
        if self._direct_messages is not None:
            self._direct_messages._bind(self, self._send, self._direct_failure)
        self._target = asyncio.get_running_loop().create_future()
        if self._reader is None:
            self._reader = asyncio.create_task(self._read())
        await self._send(
            {
                "type": "inbox_subscribe",
                "consumer": self._consumer,
                "after": after,
                **({"read_receipts": True} if self._read_progress is not None else {}),
                **({"direct_messages": True} if self._direct_messages is not None else {}),
            }
            if inbox
            else {
                "type": "subscribe",
                "channel": channel,
                "after": after,
                **({"read_receipts": True} if self._read_progress is not None else {}),
                **({"direct_messages": True} if self._direct_messages is not None else {}),
                **({"filter": dict(self._event_filter.values)} if self._event_filter is not None else {}),
                **({"subscription": self._durable_subscription} if self._durable_subscription is not None else {}),
                **(self._presence._subscribe_options() if self._presence else {}),
            }
        )
        while not self._target.done():
            self._changed.clear()
            if self._failure is not None:
                raise self._failure
            if self._target.done():
                break
            await self._changed.wait()
        return self._target.result()

    def _bounds(self, frame: dict) -> RealtimeRecoveryTarget:
        oldest, latest = frame.get("oldest_sequence", 0), frame.get("latest_sequence", 0)
        _sequence(oldest)
        _sequence(latest)
        if not 1 <= oldest <= latest + 1:
            raise RealtimeRecoveryError("invalid_target")
        return RealtimeRecoveryTarget(
            oldest_sequence=oldest,
            latest_sequence=latest,
            resume_sequence=self._resume_sequence,
            resync_required=self._needs_resync,
            filter_fingerprint=(
                self._event_filter.fingerprint if self._event_filter and self._event_filter.values else None
            ),
        )

    def _direct_failure(self, error: Exception) -> None:
        if self._direct_messages is not None:
            self._direct_messages._invalidate(self)
        self._failure = error
        self.backpressure._invalidate(self)
        self._changed.set()
        if self._presence is not None:
            self._presence._invalidate(self)
        if self._read_progress is not None:
            self._read_progress._invalidate(self)

    async def _frame(self, frame: dict) -> None:
        kind = frame.get("type")
        if kind in ("direct_message", "direct_message_acknowledged", "direct_message_ack_failed"):
            if self._direct_messages is None:
                raise RealtimeRecoveryError("unexpected_direct_message")
            self._direct_messages._receive(self, frame)
            return
        if kind == "read_receipt":
            if frame.get("inbox", False) != self._inbox or not self._inbox and frame.get("channel") != self._channel:
                raise RealtimeRecoveryError("unexpected_stream")
            if not self._active:
                raise RealtimeRecoveryError("receipt_before_subscription")
            if self._read_progress is not None:
                self._read_progress._receive(self, frame)
            if self._target is not None and not self._target.done():
                self._target.set_result(self._bounds(frame))
            return
        if kind == "read_error":
            if frame.get("inbox", False) != self._inbox or not self._inbox and frame.get("channel") != self._channel:
                raise RealtimeRecoveryError("unexpected_stream")
            if self._read_progress is not None:
                self._read_progress._rejected(self, frame.get("code"))
            if self._target is not None and not self._target.done():
                if frame.get("code") in ("read_rate_limited", "read_progress_unavailable", "read_relay_unavailable"):
                    raise RealtimeRecoveryRetry()
                raise RealtimeRecoveryError("read_state_rejected")
            return
        if self._inbox:
            if frame.get("consumer") != self._consumer:
                raise RealtimeRecoveryError("unexpected_stream")
        elif frame.get("channel") != self._channel:
            raise RealtimeRecoveryError("unexpected_stream")
        if not self._inbox and (kind in ("presence", "signal") or kind == "error" and frame.get("code") in _REJECTIONS):
            if not self._active:
                raise RealtimeRecoveryError("ephemeral_before_subscription")
            if self._presence is not None:
                self._presence._receive(self, frame)
            return
        prefix = "inbox_" if self._inbox else ""
        if kind == prefix + "subscribed":
            checkpoint = frame.get("sequence", 0)
            _sequence(checkpoint)
            if self._durable_subscription is not None:
                if self._active or frame.get("subscription") != self._durable_subscription:
                    raise RealtimeRecoveryError("unexpected_checkpoint")
                self._resume_sequence = checkpoint
                self._confirm(checkpoint)
            elif self._active or checkpoint > self._received or not self._inbox and checkpoint != self._received:
                raise RealtimeRecoveryError("unexpected_checkpoint")
            self._needs_resync = False
            self._received = self._acked = checkpoint
            self._active = True
            await self._send(
                {"type": "inbox_read_state"} if self._inbox else {"type": "read_state", "channel": self._channel}
            )
        elif kind == prefix + "resync_required":
            if self._durable_subscription is not None and frame.get("subscription") != self._durable_subscription:
                raise RealtimeRecoveryError("unexpected_subscription")
            if self._reset is not None and not self._reset.done():
                raise RealtimeRecoveryRetry()
            self._needs_resync = True
            if self._active:
                raise RealtimeRecoveryRetry()
            if self._target is not None and not self._target.done():
                self._target.set_result(self._bounds(frame))
        elif kind == "subscription_reset" and self._durable_subscription is not None:
            sequence = frame.get("sequence", 0)
            _sequence(sequence)
            if (
                frame.get("subscription") != self._durable_subscription
                or self._reset is None
                or self._reset.done()
                or sequence != self._reset_position
            ):
                raise RealtimeRecoveryError("unexpected_reset")
            self._confirm(sequence)
            self._reset.set_result(None)
        elif kind == "inbox_reset" and self._inbox:
            if self._reset is None or self._reset.done() or frame.get("sequence", 0) != self._reset_position:
                raise RealtimeRecoveryError("unexpected_reset")
            self._reset.set_result(None)
        elif kind == "inbox_checkpoint":
            sequence = frame.get("sequence")
            _sequence(sequence)
            if (
                not self._inbox
                or not self._active
                or frame.get("code") != "message_expired"
                or sequence != self._received + 1
            ):
                raise RealtimeRecoveryError("unexpected_inbox_checkpoint")
            _notify_expiry(
                self._on_message_expired,
                RealtimeMessageExpiry(inbox=True, consumer=self._consumer, sequence=sequence, transport="websocket"),
            )
            self._enqueue(RealtimeRecoveryEvent(sequence=sequence, data=None, checkpoint=True))
            self._received = sequence
            if self._read_progress is not None:
                self._read_progress._new_position(self, sequence)
        elif kind == "checkpoint":
            sequence = frame.get("sequence")
            _sequence(sequence)
            if (
                self._inbox
                or not self._active
                or frame.get("code") != "message_expired"
                and (self._event_filter is None or not self._event_filter.values)
                or sequence != self._received + 1
            ):
                raise RealtimeRecoveryError("unexpected_filter_checkpoint")
            if frame.get("code") == "message_expired":
                _notify_expiry(
                    self._on_message_expired,
                    RealtimeMessageExpiry(channel=self._channel, sequence=sequence, transport="websocket"),
                )
            self._enqueue(RealtimeRecoveryEvent(sequence=sequence, data=None, checkpoint=True))
            self._received = sequence
            if self._read_progress is not None:
                self._read_progress._new_position(self, sequence)
        elif kind == prefix + "message":
            if not self._active:
                raise RealtimeRecoveryError("message_before_subscription")
            sequence = frame.get("sequence")
            _sequence(sequence)
            if sequence != self._received + 1:
                raise RealtimeRecoveryError("sequence_gap")
            message_id = frame.get("message_id")
            encoded = frame.get("data_base64", "")
            binary = frame.get("binary", False)
            version, event = frame.get("version", 1), frame.get("message_event", "created")
            deleted, target = frame.get("deleted", False), frame.get("target_message_id")
            metadata = frame.get("metadata") or {}
            if (
                not isinstance(message_id, str)
                or not message_id
                or len(message_id) > 256
                or not isinstance(encoded, str)
                or type(binary) is not bool
                or type(version) is not int
                or not 1 <= version <= 2**63 - 1
                or event not in ("created", "updated", "deleted")
                or type(deleted) is not bool
                or target is not None
                and not isinstance(target, str)
                or not isinstance(metadata, dict)
                or len(metadata) > 8
                or any(not isinstance(k, str) or not isinstance(v, str) for k, v in metadata.items())
            ):
                raise RealtimeRecoveryError("invalid_message")
            if self._event_filter is not None and any(
                key not in metadata or metadata[key] != value for key, value in self._event_filter.values.items()
            ):
                raise RealtimeRecoveryError("filter_match_mismatch")
            try:
                payload = base64.b64decode(encoded, validate=True)
            except (ValueError, TypeError):
                raise RealtimeRecoveryError("invalid_payload") from None
            if len(payload) > 4096:
                raise RealtimeRecoveryError("invalid_payload")
            expiry = frame.get("expires_at")
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
            message = RealtimeWebSocketMessage(
                expires_at=expiry,
                message_id=message_id,
                data=payload,
                binary=binary,
                version=version,
                event="deleted" if deleted else event,
                deleted=deleted or event == "deleted",
                target_message_id=target,
                metadata=metadata,
            )
            self._enqueue(RealtimeRecoveryEvent(sequence=sequence, data=message))
            self._received = sequence
            if self._read_progress is not None:
                self._read_progress._new_position(self, sequence)
        elif kind == prefix + "acknowledged":
            sequence = frame.get("sequence", 0)
            _sequence(sequence)
            if sequence > self._acked:
                raise RealtimeRecoveryError("invalid_acknowledgement")
            if self._durable_subscription is not None:
                if frame.get("subscription") != self._durable_subscription:
                    raise RealtimeRecoveryError("unexpected_subscription")
                if self._confirmed_sequence is None or sequence >= self._confirmed_sequence:
                    self._confirm(sequence)
                if (
                    self._pending_ack is not None
                    and not self._pending_ack.done()
                    and sequence == self._pending_sequence
                ):
                    self._pending_ack.set_result(None)
        elif kind in ("error", "inbox_error", "read_error"):
            if frame.get("code") in ("history_read_failed", "checkpoint_write_failed"):
                raise RealtimeRecoveryRetry()
            raise RealtimeRecoveryError("subscription_rejected")
        else:
            raise RealtimeRecoveryError("unexpected_frame")

    async def _read(self) -> None:
        try:
            async for raw in self._socket:
                if not isinstance(raw, str):
                    raise RealtimeRecoveryError("invalid_frame")
                frame = json.loads(raw, parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
                if not isinstance(frame, dict):
                    raise RealtimeRecoveryError("invalid_frame")
                await self._frame(frame)
                self._changed.set()
            self._failure = RealtimeRecoveryRetry()
        except asyncio.CancelledError:
            raise
        except (RealtimeRecoveryRetry, RealtimeRecoveryError) as exc:
            self._failure = exc
        except Exception as exc:
            from websockets.exceptions import ConnectionClosed

            self._failure = (
                RealtimeRecoveryRetry()
                if isinstance(exc, ConnectionClosed) and exc.code not in (1002, 1003, 1007, 1008, 1009)
                else RealtimeRecoveryError("invalid_frame")
            )
        finally:
            self.backpressure._invalidate(self)
            if self._direct_messages is not None:
                self._direct_messages._invalidate(self)
            if self._presence is not None:
                self._presence._invalidate(self)
            if self._read_progress is not None:
                self._read_progress._invalidate(self)
            self._changed.set()

    async def _next(self) -> RealtimeRecoveryEvent:
        while True:
            self._changed.clear()
            if self._closed:
                raise RealtimeRecoveryError("session_closed")
            if self._failure is not None:
                raise self._failure
            if self._queue:
                event = self._queue.popleft()
                self._last_queue_time = self.backpressure._pop(self)
                return event
            if self._drain_cutoff is not None:
                if self._sent_ack is None or self._sent_ack < self._drain_cutoff:
                    await self._ack(self._drain_cutoff)
                raise _RecoveryDrained()
            await self._changed.wait()

    async def _ack(self, sequence: int) -> None:
        self._acked = sequence
        if self._durable_subscription is not None:
            self._pending_sequence = sequence
            self._pending_ack = asyncio.get_running_loop().create_future()
        await self._send(
            {"type": "inbox_ack", "consumer": self._consumer, "sequence": sequence}
            if self._inbox
            else {"type": "ack", "channel": self._channel, "sequence": sequence}
        )
        self._sent_ack = sequence
        if self._pending_ack is not None:
            try:
                async with asyncio.timeout(self._checkpoint_timeout):
                    await self._wait_control(self._pending_ack)
            except TimeoutError:
                raise RealtimeRecoveryRetry() from None
            finally:
                self._pending_ack = None
                self._pending_sequence = None

    async def replay(self, *, after: int, through: int) -> AsyncIterator[RealtimeRecoveryEvent]:
        _sequence(after)
        _sequence(through)
        if self._iterating or after > through:
            raise RealtimeRecoveryError("invalid_replay_range")
        self._iterating = True
        try:
            if not self._active:
                if self._inbox or self._durable_subscription is not None:
                    self._reset_position = after
                    self._reset = asyncio.get_running_loop().create_future()
                    await self._send(
                        {"type": "inbox_reset", "consumer": self._consumer, "sequence": after}
                        if self._inbox
                        else {
                            "type": "reset",
                            "channel": self._channel,
                            "subscription": self._durable_subscription,
                            "sequence": after,
                        }
                    )
                    await self._wait_control(self._reset)
                await self.subscribe(channel=self._channel, inbox=self._inbox, after=after)
            position = max(after, self._resume_sequence or 0) if self._durable_subscription is not None else after
            if position > through:
                raise RealtimeRecoveryError("invalid_replay_range")
            while position < through:
                event = await self._next()
                if event.sequence > through:
                    if not event.checkpoint:
                        raise RealtimeRecoveryError("sequence_gap")
                    self._queue.appendleft(event)
                    self.backpressure._append(self, front_time=self._last_queue_time)
                    event = RealtimeRecoveryEvent(sequence=through, data=None, checkpoint=True)
                if event.sequence <= after:
                    await self._ack(event.sequence)
                    continue
                self._yielded_sequence = event.sequence
                yield event
                position = event.sequence
                await self._ack(position)
                self._yielded_sequence = None
        finally:
            self._iterating = False

    async def events(self) -> AsyncIterator[RealtimeRecoveryEvent]:
        if self._iterating or not self._active:
            raise RealtimeRecoveryError("invalid_session_state")
        self._iterating = True
        try:
            while True:
                event = await self._next()
                self._yielded_sequence = event.sequence
                yield event
                await self._ack(event.sequence)
                self._yielded_sequence = None
        finally:
            self._iterating = False

    async def aclose(self) -> None:
        self.backpressure._invalidate(self)
        if self._direct_messages is not None:
            self._direct_messages._invalidate(self)
            await self._direct_messages._join(self)
        if self._presence is not None:
            self._presence._invalidate(self)
        if self._read_progress is not None:
            self._read_progress._invalidate(self)
            await self._read_progress._join_refresh()
        self._closed = True
        self._changed.set()
        if self._reader is not None:
            self._reader.cancel()
            await asyncio.gather(self._reader, return_exceptions=True)
        await self._socket.close()
