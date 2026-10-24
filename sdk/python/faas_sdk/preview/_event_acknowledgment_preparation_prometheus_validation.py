"""Bounded native-container validation for current preparation metric snapshots."""

from __future__ import annotations

import re
from typing import Any, NoReturn

from ._event_acknowledgment_preparation_metric_events import _KINDS, _MAX, _OUTCOMES, _REASONS, _STAGES
from .event_acknowledgment_preparation_metrics import (
    EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS,
    EventAcknowledgmentPreparationHistogram,
)

_DEFAULT_PREFIX = "gregale_event_acknowledgment_preparation"
_INVALID_EXPOSITION = "invalid acknowledgment preparation metrics exposition input"
_SNAPSHOT_KEYS = (
    "event_counts",
    "operation_outcomes",
    "preparation_outcomes",
    "local_commits",
    "renewal_failures",
    "unknown_commits_by_stage",
    "preparation_duration",
    "operation_duration",
    "saturated",
)


def _invalid() -> NoReturn:
    raise ValueError(_INVALID_EXPOSITION)


def _data(value: object, keys: tuple[str, ...]) -> dict[str, Any]:
    # Reject subclasses/mappings without calling user-defined attributes or methods.
    if type(value) is not dict or len(value) != len(keys):
        _invalid()
    result = {}
    for key, item in dict.items(value):
        if type(key) is not str or key not in keys:
            _invalid()
        result[key] = item
    return result


def _count(value: object) -> int:
    if type(value) is not int or not 0 <= value <= _MAX:
        _invalid()
    return value


def _flag(value: object) -> bool:
    if type(value) is not bool:
        _invalid()
    return value


def _counters(value: object, labels: tuple[str, ...]) -> dict[str, int]:
    data = _data(value, labels)
    return {key: _count(data[key]) for key in labels}


def _histogram(value: object) -> EventAcknowledgmentPreparationHistogram:
    data = _data(value, ("buckets", "count", "sum_ms", "saturated"))
    raw = data["buckets"]
    bounds = (*EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS, None)
    if type(raw) is not list or len(raw) != len(bounds):
        _invalid()
    buckets = []
    previous = 0
    for index, bound in enumerate(bounds):
        bucket = _data(raw[index], ("upper_bound_ms", "count"))
        if bound is None:
            if bucket["upper_bound_ms"] is not None:
                _invalid()
        elif _count(bucket["upper_bound_ms"]) != bound:
            _invalid()
        current = _count(bucket["count"])
        if current < previous:
            _invalid()
        previous = current
        buckets.append({"upper_bound_ms": bound, "count": current})
    total, sum_ms, saturated = _count(data["count"]), _count(data["sum_ms"]), _flag(data["saturated"])
    if previous != total or total == 0 and sum_ms != 0:
        _invalid()
    return {"buckets": buckets, "count": total, "sum_ms": sum_ms, "saturated": saturated}


def _validated(snapshot: object, prefix: object) -> tuple[str, dict[str, Any]]:
    if (
        type(prefix) is not str
        or not 1 <= len(prefix) <= 128
        or re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", prefix) is None
    ):
        _invalid()
    data = _data(snapshot, _SNAPSHOT_KEYS)
    renewals = _data(data["renewal_failures"], ("local", "platform"))
    preparation = _histogram(data["preparation_duration"])
    operation = _histogram(data["operation_duration"])
    saturated = _flag(data["saturated"])
    if not saturated and (preparation["saturated"] or operation["saturated"]):
        _invalid()
    return prefix, {
        "event_counts": _counters(data["event_counts"], _KINDS),
        "operation_outcomes": _counters(data["operation_outcomes"], _OUTCOMES),
        "preparation_outcomes": _counters(data["preparation_outcomes"], ("returned", "threw")),
        "local_commits": _counters(data["local_commits"], ("new_receipt", "replayed_receipt")),
        "renewal_failures": {
            "local": _counters(renewals["local"], _REASONS),
            "platform": _counters(renewals["platform"], _REASONS),
        },
        "unknown_commits_by_stage": _counters(data["unknown_commits_by_stage"], _STAGES),
        "preparation_duration": preparation,
        "operation_duration": operation,
        "saturated": saturated,
    }
