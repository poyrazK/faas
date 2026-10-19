"""Pure text-0.0.4 recovery gauges, sharing Node metric names and HELP strings."""

from __future__ import annotations

import re

from .event_acknowledgment_intent_health_metrics import (
    _INVALID,
    EventAcknowledgmentIntentHealthMetricsSnapshot,
    _instant_ns,
    _validated_snapshot,
)

EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"
_DEFAULT_PREFIX = "gregale_event_acknowledgment_recovery"


def _seconds(nanoseconds: int) -> str:
    whole, fraction = divmod(abs(nanoseconds), 1000000000)
    sign = "-" if nanoseconds < 0 else ""
    return f"{sign}{whole}" if fraction == 0 else f"{sign}{whole}.{fraction:09d}".rstrip("0")


def format_event_acknowledgment_intent_health_metrics(
    snapshot: EventAcknowledgmentIntentHealthMetricsSnapshot, *, prefix: str = _DEFAULT_PREFIX
) -> str:
    """Format a fresh snapshot per scrape. Missing sample gauges are omitted, never zero-filled."""
    validated = None
    try:
        if (
            type(prefix) is not str
            or not 1 <= len(prefix) <= 128
            or re.fullmatch(r"[a-zA-Z_][a-zA-Z0-9_]*", prefix) is None
        ):
            raise ValueError(_INVALID)
        validated = (_validated_snapshot(snapshot), prefix)
    except Exception:
        pass
    del snapshot, prefix
    if validated is None:
        raise ValueError(_INVALID)
    data, metric_prefix = validated
    lines: list[str] = []

    def gauge(suffix: str, help_text: str, value: int | float | str) -> None:
        name = f"{metric_prefix}_{suffix}"
        lines.extend((f"# HELP {name} {help_text}", f"# TYPE {name} gauge", f"{name} {value}"))

    gauge(
        "read_success",
        "Latest read supplied valid scoped metadata; zero before a read or after failure.",
        int(data.read_success),
    )
    gauge(
        "evidence_eligible",
        "Complete fresh advancing evidence remains fresh at snapshot time.",
        int(data.evidence_eligible),
    )
    gauge(
        "max_observation_age_seconds",
        "Configured maximum observation age in seconds.",
        data.max_observation_age_seconds,
    )
    if data.health is not None:
        h = data.health
        gauge("complete", "Latest bounded sample has complete coverage.", int(h.complete))
        gauge("truncated", "Latest sample exceeded the scan limit; counts are lower bounds.", int(h.truncated))
        gauge("scan_limit", "Maximum sampled unfinished intents.", h.scan_limit)
        gauge("invalid_rows", "Sampled rows with invalid timestamp metadata.", h.invalid_rows)
        gauge("outstanding", "Sampled retained unfinished intents including parked work.", h.outstanding)
        gauge("due", "Sampled due intents excluding live claims and parked work.", h.due)
        gauge("stalled", "Sampled stalled eligible intents excluding parked work.", h.stalled)
        gauge("parked", "Sampled retained intents whose recovery claim budget is exhausted.", h.parked)
        gauge(
            "observation_timestamp_seconds",
            "Database observation time in Unix seconds.",
            _seconds(_instant_ns(h.observed_at)),
        )
        gauge(
            "observation_age_seconds",
            "Conservative observation age using wall and local monotonic elapsed time.",
            h.observation_age_seconds,
        )
    return "\n".join(lines) + "\n"
