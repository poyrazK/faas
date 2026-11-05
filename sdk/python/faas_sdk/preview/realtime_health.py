"""Pure UI projection of independent realtime observations, without transport policy."""

from __future__ import annotations

import math
from dataclasses import dataclass
from datetime import datetime
from typing import Literal

from .realtime_catch_up import RealtimeCatchUpStatus
from .realtime_connection_status import RealtimeConnectionQuality, RealtimeConnectionStatus
from .realtime_presence import RealtimePresenceSummary, _channel, summarize_realtime_activity
from .realtime_read_progress import RealtimeUnreadBadge, RealtimeUnreadSummary, format_realtime_unread_badge
from .realtime_subscriptions import RealtimeSubscriptionStatus, RealtimeSubscriptionSummary, _identity


@dataclass(frozen=True, kw_only=True)
class RealtimeHealthSummary:
    state: Literal[
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
    ]
    label: str
    reasons: tuple[str, ...]
    connection: RealtimeConnectionStatus
    quality: RealtimeConnectionQuality | None
    unread_badge: RealtimeUnreadBadge | None
    presence_ready: bool | None
    waiting_presence_channels: tuple[str, ...]
    viewer_count: int
    history_unavailable: bool
    catch_up: tuple[RealtimeCatchUpStatus, ...] = ()
    subscriptions: RealtimeSubscriptionSummary | None = None


def summarize_realtime_health(
    connection: RealtimeConnectionStatus,
    *,
    quality: RealtimeConnectionQuality | None = None,
    unread: RealtimeUnreadSummary | None = None,
    presence: tuple[RealtimePresenceSummary, ...] = (),
    max_unread_count: int = 99,
    catch_up: tuple[RealtimeCatchUpStatus, ...] = (),
    subscriptions: RealtimeSubscriptionSummary | None = None,
) -> RealtimeHealthSummary:
    """Combine current observations from one endpoint/account/connection attempt.

    Omitted optional observations are untracked, not a claim that data is fresh.
    A live state means the supplied observations are ready, not global completeness.
    """
    if (
        not isinstance(connection, RealtimeConnectionStatus)
        or connection.state not in ("offline", "connecting", "reconnecting", "connected")
        or type(connection.attempt_id) is not int
        or connection.attempt_id < 0
        or type(connection.reconnect_attempts) is not int
        or connection.reconnect_attempts < 0
        or type(connection.views_stale) is not bool
        or connection.views_stale != (connection.state != "connected")
    ):
        raise ValueError("Invalid connection status")
    if connection.last_connected_at is not None and (
        not isinstance(connection.last_connected_at, datetime)
        or connection.last_connected_at.tzinfo is None
        or connection.last_connected_at.utcoffset() is None
    ):
        raise ValueError("Connection timestamps must be timezone-aware")
    if quality is not None:
        if (
            not isinstance(quality, RealtimeConnectionQuality)
            or quality.state not in ("unknown", "healthy", "degraded", "offline")
            or type(quality.attempt_id) is not int
            or quality.attempt_id < 0
        ):
            raise ValueError("Invalid connection quality")
        for value in (quality.latency_milliseconds, quality.last_frame_age_seconds, quality.heartbeat_pending_seconds):
            if value is not None and (
                isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0
            ):
                raise ValueError("Invalid connection-quality observation")
    if not isinstance(presence, (tuple, list)) or len(presence) > 8192:
        raise ValueError("Invalid presence observations")
    waiting, channels = [], set()
    viewers = 0
    for item in presence:
        if not isinstance(item, RealtimePresenceSummary) or type(item.ready) is not bool:
            raise ValueError("Invalid presence readiness")
        _channel(item.channel)
        if item.channel in channels:
            raise ValueError("Duplicate presence channel")
        channels.add(item.channel)
        viewers += summarize_realtime_activity(item).viewer_count
        if viewers > 8192:
            raise ValueError("Health presence capacity reached")
        if not item.ready or connection.views_stale:
            waiting.append(item.channel)
    if type(max_unread_count) is not int or not 1 <= max_unread_count <= 999999:
        raise ValueError("max_unread_count must be an integer between 1 and 999999")
    badge = format_realtime_unread_badge(unread, max_count=max_unread_count) if unread is not None else None
    reasons = []
    current_quality = quality if quality is not None and quality.attempt_id == connection.attempt_id else None
    if quality is not None and (current_quality is None or quality.state in ("unknown", "offline")):
        reasons.append("quality_unknown")
    elif current_quality is not None and current_quality.state == "degraded":
        reasons.append("connection_degraded")
    if badge is not None and badge.updating:
        reasons.append("unread_unverified")
    if waiting:
        reasons.append("presence_pending")
    if badge is not None and badge.history_unavailable:
        reasons.append("history_unavailable")
    if not isinstance(catch_up, (tuple, list)) or len(catch_up) > 8192:
        raise ValueError("Invalid catch-up observations")
    seen_streams = set()
    for item in catch_up:
        if not isinstance(item, RealtimeCatchUpStatus) or item.state not in (
            "idle",
            "waiting_target",
            "catching_up",
            "caught_up",
            "resync_required",
            "stalled",
        ):
            raise ValueError("Invalid catch-up status")
        for value in (item.progress_age_seconds, item.stall_seconds):
            if value is not None and (
                isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0
            ):
                raise ValueError("Invalid catch-up elapsed time")
        key = (item.inbox, item.channel)
        if key in seen_streams:
            raise ValueError("Duplicate catch-up stream")
        seen_streams.add(key)
        if item.attempt_id != connection.attempt_id or item.state in ("idle", "waiting_target"):
            if "catch_up_unknown" not in reasons:
                reasons.append("catch_up_unknown")
        elif item.state == "resync_required":
            if "resync_required" not in reasons:
                reasons.append("resync_required")
        elif item.state == "stalled":
            if "catch_up_stalled" not in reasons:
                reasons.append("catch_up_stalled")
        elif item.state == "catching_up" and "catching_up" not in reasons:
            reasons.append("catching_up")
    if subscriptions is not None:
        if not isinstance(subscriptions, RealtimeSubscriptionSummary) or type(subscriptions.ready) is not bool:
            raise ValueError("Invalid subscription summary")
        if not isinstance(subscriptions.subscriptions, (tuple, list)) or len(subscriptions.subscriptions) > 8192:
            raise ValueError("Invalid subscription observations")
        if (
            type(subscriptions.attempt_id) is not int
            or subscriptions.attempt_id < 0
            or type(subscriptions.generation) is not int
            or subscriptions.generation < 0
        ):
            raise ValueError("Invalid subscription generation")
        identities = set()
        for entry in subscriptions.subscriptions:
            if (
                not isinstance(entry, RealtimeSubscriptionStatus)
                or entry.state not in ("pending", "subscribed", "denied", "resync_required")
                or type(entry.acknowledgement_missing) is not bool
            ):
                raise ValueError("Invalid subscription status")
            key = _identity(entry.channel, entry.inbox)
            if key in identities:
                raise ValueError("Duplicate subscription stream")
            identities.add(key)
        if subscriptions.ready and (
            subscriptions.generation == 0
            or subscriptions.attempt_id == 0
            or any(entry.state != "subscribed" for entry in subscriptions.subscriptions)
        ):
            raise ValueError("Inconsistent subscription readiness")
        if subscriptions.attempt_id != connection.attempt_id:
            reasons.append("subscriptions_pending")
        else:
            states = {entry.state for entry in subscriptions.subscriptions}
            if "denied" in states:
                reasons.append("subscription_denied")
            if "resync_required" in states and "resync_required" not in reasons:
                reasons.append("resync_required")
            if not subscriptions.ready or "pending" in states:
                reasons.append("subscriptions_pending")
            if any(entry.acknowledgement_missing for entry in subscriptions.subscriptions):
                reasons.append("subscription_ack_missing")
    if connection.state != "connected":
        state = connection.state
    elif "resync_required" in reasons:
        state = "resync_required"
    elif "subscription_denied" in reasons:
        state = "subscription_failed"
    elif "catch_up_stalled" in reasons:
        state = "catch_up_stalled"
    elif "connection_degraded" in reasons:
        state = "delayed"
    elif "catching_up" in reasons:
        state = "catching_up"
    elif any(
        reason in reasons
        for reason in (
            "quality_unknown",
            "unread_unverified",
            "presence_pending",
            "catch_up_unknown",
            "subscriptions_pending",
        )
    ):
        state = "updating"
    else:
        state = "live"
    labels = {
        "offline": "Offline",
        "connecting": "Connecting",
        "reconnecting": "Reconnecting",
        "delayed": "Delayed",
        "updating": "Updating",
        "live": "Live",
        "catching_up": "Catching up",
        "resync_required": "Resync required",
        "catch_up_stalled": "Catch-up stalled",
        "subscription_failed": "Subscription failed",
    }
    return RealtimeHealthSummary(
        state=state,
        label=labels[state],
        reasons=tuple(reasons),
        connection=connection,
        quality=current_quality,
        unread_badge=badge,
        presence_ready=not waiting if presence else None,
        waiting_presence_channels=tuple(sorted(waiting)),
        viewer_count=viewers,
        history_unavailable=(badge.history_unavailable if badge is not None else False) or "resync_required" in reasons,
        catch_up=tuple(catch_up),
        subscriptions=subscriptions,
    )
