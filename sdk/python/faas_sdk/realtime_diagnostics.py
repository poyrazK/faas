"""Explicitly whitelisted local diagnostics; no payloads, credentials or uploads."""

from __future__ import annotations

import json
import math
from datetime import datetime, timezone
from typing import Any

from .realtime_backpressure import RealtimeBackpressureStatus, RealtimeProcessingStatus
from .realtime_expiry import RealtimeExpiryStatus
from .realtime_health import RealtimeHealthSummary, summarize_realtime_health
from .realtime_read_progress import RealtimeUnreadSummary, format_realtime_unread_badge
from .realtime_timeline import _TIMELINE_STATES, RealtimeIncidentTimeline
from .realtime_transport import RealtimeTransportStatus

_REASONS = {
    "quality_unknown",
    "connection_degraded",
    "unread_unverified",
    "presence_pending",
    "history_unavailable",
    "catch_up_unknown",
    "resync_required",
    "catching_up",
    "catch_up_stalled",
    "subscriptions_pending",
    "subscription_denied",
    "subscription_ack_missing",
}
_STATES = {
    "offline",
    "connecting",
    "reconnecting",
    "delayed",
    "updating",
    "live",
    "catching_up",
    "resync_required",
    "catch_up_stalled",
    "subscription_failed",
}


def _elapsed(value: float | None) -> float | None:
    if value is not None and (
        isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0
    ):
        raise ValueError("Invalid diagnostic elapsed value")
    return value


def _integer(value: int | None) -> str | None:
    if value is not None and (type(value) is not int or not 0 <= value <= 2**63 - 1):
        raise ValueError("Invalid diagnostic sequence")
    return str(value) if value is not None else None


def export_realtime_diagnostics(
    health: RealtimeHealthSummary,
    *,
    unread: RealtimeUnreadSummary | None = None,
    include_identifiers: bool = False,
    max_entries: int = 256,
    timeline: RealtimeIncidentTimeline | None = None,
    transport: RealtimeTransportStatus | None = None,
    backpressure: RealtimeBackpressureStatus | None = None,
    processing: RealtimeProcessingStatus | None = None,
    expiry: RealtimeExpiryStatus | None = None,
) -> str:
    """Return bounded version-1 JSON. Identifiers are per-export aliases by default.

    Capture current observations from one endpoint/account. This function never
    reads a transport, file, environment variable, payload or exception object.
    """
    if type(include_identifiers) is not bool:
        raise ValueError("include_identifiers must be boolean")
    if type(max_entries) is not int or not 1 <= max_entries <= 8192:
        raise ValueError("max_entries must be an integer between 1 and 8192")
    if not isinstance(health, RealtimeHealthSummary) or health.state not in _STATES:
        raise ValueError("Expected a valid realtime health summary")
    if (
        not isinstance(health.reasons, (tuple, list))
        or len(health.reasons) > len(_REASONS)
        or any(reason not in _REASONS for reason in health.reasons)
    ):
        raise ValueError("Invalid diagnostic health reasons")
    # Reuse stage validation, but never serialize entire objects or free-form labels/codes.
    summarize_realtime_health(
        health.connection, quality=health.quality, catch_up=health.catch_up, subscriptions=health.subscriptions
    )
    if unread is not None:
        format_realtime_unread_badge(unread)
    if type(health.presence_ready) is not bool and health.presence_ready is not None:
        raise ValueError("Invalid presence readiness")
    if (
        type(health.viewer_count) is not int
        or not 0 <= health.viewer_count <= 8192
        or type(health.history_unavailable) is not bool
    ):
        raise ValueError("Invalid diagnostic health counts")
    aliases: dict[tuple[str, str], str] = {}
    counters = {"channel": 0, "reader": 0}

    def identity(kind: str, value: str | None) -> str | None:
        if value is None:
            return None
        limit = 256 if kind == "channel" else 64
        if not isinstance(value, str) or not value or len(value.encode()) > limit:
            raise ValueError("Invalid diagnostic identity")
        if include_identifiers:
            return value
        key = kind, value
        if key not in aliases:
            counters[kind] += 1
            aliases[key] = f"{kind}_{counters[kind]}"
        return aliases[key]

    truncated: dict[str, int] = {}

    def bounded(name: str, values: Any) -> Any:
        if not isinstance(values, (tuple, list)) or len(values) > 8192:
            raise ValueError("Invalid diagnostic collection")
        omitted = max(0, len(values) - max_entries)
        if omitted:
            truncated[name] = omitted
        return values[:max_entries]

    connection = health.connection
    last_connected = connection.last_connected_at
    result: dict[str, Any] = {
        "format_version": 1,
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "identifiers_redacted": not include_identifiers,
        "health": {
            "state": health.state,
            "reasons": list(health.reasons),
            "history_unavailable": health.history_unavailable,
        },
        "connection": {
            "state": connection.state,
            "attempt_id": connection.attempt_id,
            "reconnect_attempts": connection.reconnect_attempts,
            "views_stale": connection.views_stale,
            "last_connected_at": last_connected.astimezone(timezone.utc).isoformat() if last_connected else None,
        },
        "quality": None,
        "subscriptions": None,
        "catch_up": [],
        "unread": None,
        "presence": {
            "ready": health.presence_ready,
            "viewer_count": health.viewer_count,
            "waiting_channels": [
                identity("channel", channel) for channel in bounded("presence", health.waiting_presence_channels)
            ],
        },
        "truncated": truncated,
    }
    if health.quality is not None:
        quality = health.quality
        result["quality"] = {
            "state": quality.state,
            "attempt_id": quality.attempt_id,
            "latency_milliseconds": _elapsed(quality.latency_milliseconds),
            "last_frame_age_seconds": _elapsed(quality.last_frame_age_seconds),
            "heartbeat_pending_seconds": _elapsed(quality.heartbeat_pending_seconds),
        }
    if health.subscriptions is not None:
        summary = health.subscriptions
        result["subscriptions"] = {
            "attempt_id": summary.attempt_id,
            "generation": summary.generation,
            "ready": summary.ready,
            "entries": [
                {
                    "channel": identity("channel", item.channel),
                    "inbox": item.inbox,
                    "state": item.state,
                    "acknowledgement_missing": item.acknowledgement_missing,
                    "pending_age_seconds": _elapsed(item.pending_age_seconds),
                }
                for item in bounded("subscriptions", summary.subscriptions)
            ],
        }
    for item in bounded("catch_up", health.catch_up):
        if (
            type(item.inbox) is not bool
            or type(item.attempt_id) is not int
            or type(item.generation) is not int
            or item.attempt_id < 0
            or item.generation < 0
        ):
            raise ValueError("Invalid diagnostic catch-up identity")
        result["catch_up"].append(
            {
                "channel": identity("channel", item.channel),
                "inbox": item.inbox,
                "attempt_id": item.attempt_id,
                "generation": item.generation,
                "state": item.state,
                "processed_sequence": _integer(item.processed_sequence),
                "target_sequence": _integer(item.target_sequence),
                "oldest_sequence": _integer(item.oldest_sequence),
                "latest_sequence": _integer(item.latest_sequence),
                "progress_age_seconds": _elapsed(item.progress_age_seconds),
                "stall_seconds": _elapsed(item.stall_seconds),
            }
        )
    if unread is None and health.unread_badge is not None:
        badge = health.unread_badge
        if (
            type(badge.total_unread) is not int
            or badge.total_unread < 0
            or type(badge.updating) is not bool
            or type(badge.history_unavailable) is not bool
        ):
            raise ValueError("Invalid diagnostic unread badge")
        result["unread"] = {
            "total_unread": str(badge.total_unread),
            "updating": badge.updating,
            "history_unavailable": badge.history_unavailable,
            "entries": None,
        }
    if unread is not None:
        stale = None if unread.stale_streams is None else set(unread.stale_streams)
        result["unread"] = {
            "reader_id": identity("reader", unread.reader_id),
            "total_unread": str(unread.total_unread),
            "complete": unread.complete,
            "inbox_missing": unread.inbox_missing,
            "missing_channels": [
                identity("channel", channel) for channel in bounded("missing_channels", unread.missing_channels)
            ],
            "entries": [
                {
                    "channel": identity("channel", item.channel),
                    "inbox": item.inbox,
                    "sequence": _integer(item.sequence),
                    "unread": _integer(item.unread),
                    "oldest_sequence": _integer(item.oldest_sequence),
                    "latest_sequence": _integer(item.latest_sequence),
                    "history_unavailable": item.history_unavailable,
                    "stale": None if stale is None else item in stale,
                }
                for item in bounded("unread", unread.streams)
            ],
        }
    result["timeline"] = None
    if timeline is not None:
        if not isinstance(timeline, RealtimeIncidentTimeline):
            raise ValueError("Expected RealtimeIncidentTimeline")
        events = timeline.snapshot()
        omitted = max(0, len(events) - max_entries)
        if omitted:
            truncated["timeline"] = omitted
        rows = []
        # Preserve newest context when reducing export detail, in chronological order.
        for event in events[-max_entries:]:
            if event.category not in _TIMELINE_STATES or event.state not in _TIMELINE_STATES[event.category]:
                raise ValueError("Invalid timeline diagnostic state")
            rows.append(
                {
                    "event_id": event.event_id,
                    "occurred_at": event.occurred_at.astimezone(timezone.utc).isoformat(),
                    "elapsed_seconds": _elapsed(event.elapsed_seconds),
                    "category": event.category,
                    "state": event.state,
                    "attempt_id": event.attempt_id,
                    "generation": event.generation,
                    "channel": identity("channel", event.channel),
                    "inbox": event.inbox,
                    "acknowledgement_missing": event.acknowledgement_missing,
                }
            )
        result["timeline"] = {"evicted_count": timeline.evicted_count, "events": rows}
    if transport is not None:
        if (
            not isinstance(transport, RealtimeTransportStatus)
            or transport.transport not in ("websocket", "sse")
            or type(transport.websocket_failures) is not int
            or not 0 <= transport.websocket_failures <= 100
            or type(transport.fallback) is not bool
            or transport.fallback != (transport.transport == "sse")
        ):
            raise ValueError("Invalid transport diagnostics")
        result["transport"] = {
            "transport": transport.transport,
            "websocket_failures": transport.websocket_failures,
            "fallback": transport.fallback,
        }
    if backpressure is not None:
        if (
            not isinstance(backpressure, RealtimeBackpressureStatus)
            or backpressure.state not in ("inactive", "healthy", "busy", "overloaded")
            or type(backpressure.active) is not bool
            or backpressure.active != (backpressure.state != "inactive")
            or type(backpressure.capacity) is not int
            or not 0 <= backpressure.capacity <= 8192
            or backpressure.active
            and backpressure.capacity == 0
            or type(backpressure.queue_depth) is not int
            or not 0 <= backpressure.queue_depth <= backpressure.capacity
            or type(backpressure.overflow_count) is not int
            or not 0 <= backpressure.overflow_count <= 2**63 - 1
            or (backpressure.oldest_queued_age_seconds is not None) != (backpressure.queue_depth > 0)
            or not backpressure.active
            and backpressure.queue_depth != 0
        ):
            raise ValueError("Invalid backpressure diagnostics")
        result["backpressure"] = {
            "state": backpressure.state,
            "active": backpressure.active,
            "queue_depth": backpressure.queue_depth,
            "capacity": backpressure.capacity,
            "oldest_queued_age_seconds": _elapsed(backpressure.oldest_queued_age_seconds),
            "overflow_count": backpressure.overflow_count,
        }
    if processing is not None:
        if (
            not isinstance(processing, RealtimeProcessingStatus)
            or type(processing.processing) is not bool
            or processing.processing != (processing.active_handler_age_seconds is not None)
        ):
            raise ValueError("Invalid processing diagnostics")
        result["processing"] = {
            "processing": processing.processing,
            "active_handler_age_seconds": _elapsed(processing.active_handler_age_seconds),
            "last_handler_duration_seconds": _elapsed(processing.last_handler_duration_seconds),
        }
    if expiry is not None:
        if not isinstance(expiry, RealtimeExpiryStatus) or any(
            type(value) is not int
            for value in (expiry.websocket_checkpoints, expiry.sse_checkpoints, expiry.inbox_checkpoints)
        ):
            raise ValueError("Invalid expiry status")
        result["expiry"] = {
            "websocket_checkpoints": _integer(expiry.websocket_checkpoints),
            "sse_checkpoints": _integer(expiry.sse_checkpoints),
            "inbox_checkpoints": _integer(expiry.inbox_checkpoints),
        }
    encoded = json.dumps(result, ensure_ascii=False, allow_nan=False, separators=(",", ":"), sort_keys=True)
    if len(encoded.encode()) > 262144:
        raise ValueError("Diagnostics exceed 256 KiB; reduce max_entries")
    return encoded
