"""Fixed-memory preparation counters and histograms; no exporter or retained events."""

from __future__ import annotations

from typing import Any, TypedDict

from ._event_acknowledgment_preparation_metric_events import (
    _INVALID_EVENT,
    _KINDS,
    _MAX,
    _OUTCOMES,
    _REASONS,
    _STAGES,
    _validated,
)
from .event_acknowledgment_preparation_events import (
    EventAcknowledgmentPreparationEvent,
    EventAcknowledgmentPreparationOutcome,
    EventAcknowledgmentPreparationReason,
    EventAcknowledgmentPreparationStage,
)

EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS = (
    1,
    5,
    10,
    25,
    50,
    100,
    250,
    500,
    1000,
    2500,
    5000,
    10000,
    30000,
    60000,
    300000,
)


class _Bucket(TypedDict):
    upper_bound_ms: int | None
    count: int


class EventAcknowledgmentPreparationHistogram(TypedDict):
    """Inclusive cumulative buckets; None is +Inf. Saturation marks clipped lower bounds."""

    buckets: list[_Bucket]
    count: int
    sum_ms: int
    saturated: bool


class _LocalCommits(TypedDict):
    new_receipt: int
    replayed_receipt: int


class _RenewalFailures(TypedDict):
    local: dict[EventAcknowledgmentPreparationReason, int]
    platform: dict[EventAcknowledgmentPreparationReason, int]


class EventAcknowledgmentPreparationMetricsSnapshot(TypedDict):
    """Detached JSON-safe cumulative observations, with zeroes for every fixed label."""

    event_counts: dict[str, int]
    operation_outcomes: dict[EventAcknowledgmentPreparationOutcome, int]
    preparation_outcomes: dict[str, int]
    local_commits: _LocalCommits
    renewal_failures: _RenewalFailures
    unknown_commits_by_stage: dict[EventAcknowledgmentPreparationStage, int]
    preparation_duration: EventAcknowledgmentPreparationHistogram
    operation_duration: EventAcknowledgmentPreparationHistogram
    saturated: bool


class _Histogram:
    __slots__ = ("counts", "sum_ms", "saturated")

    def __init__(self) -> None:
        self.counts = [0] * (len(EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS) + 1)
        self.sum_ms, self.saturated = 0, False

    def _add(self, value: int, amount: int) -> int:
        if value > _MAX - amount:
            self.saturated = True
            return _MAX
        return value + amount

    def observe(self, value: int) -> None:
        for index, bound in enumerate((*EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS, None)):
            if bound is None or value <= bound:
                self.counts[index] = self._add(self.counts[index], 1)
        self.sum_ms = self._add(self.sum_ms, value)

    def snapshot(self) -> EventAcknowledgmentPreparationHistogram:
        return {
            "buckets": [
                {"upper_bound_ms": bound, "count": count}
                for bound, count in zip((*EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS, None), self.counts)
            ],
            "count": self.counts[-1],
            "sum_ms": self.sum_ms,
            "saturated": self.saturated,
        }


class EventAcknowledgmentPreparationMetrics:
    """Cumulative observations. Serialize record/snapshot calls in one execution context."""

    __slots__ = (
        "_events",
        "_outcomes",
        "_preparations",
        "_commits",
        "_renewals",
        "_unknowns",
        "_preparation_duration",
        "_operation_duration",
        "_saturated",
    )

    def __init__(self) -> None:
        self._events = dict.fromkeys(_KINDS, 0)
        self._outcomes = dict.fromkeys(_OUTCOMES, 0)
        self._preparations = {"returned": 0, "threw": 0}
        self._commits = {"new_receipt": 0, "replayed_receipt": 0}
        self._renewals = {"local": dict.fromkeys(_REASONS, 0), "platform": dict.fromkeys(_REASONS, 0)}
        self._unknowns = dict.fromkeys(_STAGES, 0)
        self._preparation_duration, self._operation_duration = _Histogram(), _Histogram()
        self._saturated = False

    def _increment(self, counts: dict[str, int], key: str) -> None:
        if counts[key] == _MAX:
            self._saturated = True
        else:
            counts[key] += 1

    def record(self, event: EventAcknowledgmentPreparationEvent | dict[str, Any]) -> None:
        """Validate a complete event before mutation. Pass this bound method as on_event."""
        failed = False
        sample = None
        try:
            sample = _validated(event)
        except BaseException:
            failed = True
        del event
        if failed or sample is None:
            sample = None
            # No validation cause, rejected data or arbitrary getter error escapes.
            raise ValueError(_INVALID_EVENT)
        kind = sample["kind"]
        self._increment(self._events, kind)
        if kind == "preparation_finished":
            self._increment(self._preparations, sample["outcome"])
            self._preparation_duration.observe(sample["duration_ms"])
        elif kind == "renewal_failed":
            self._increment(self._renewals[sample["lease"]], sample["reason"])
        elif kind == "intent_committed":
            self._increment(self._commits, "replayed_receipt" if sample["replayed"] else "new_receipt")
        elif kind == "commit_unknown":
            self._increment(self._unknowns, sample["stage"])
        elif kind == "shutdown":
            self._increment(self._outcomes, sample["outcome"])
            self._operation_duration.observe(sample["elapsed_ms"])

    def snapshot(self) -> EventAcknowledgmentPreparationMetricsSnapshot:
        """Fresh detached JSON-safe dictionaries/lists; no export side effects or live references."""
        preparation = self._preparation_duration.snapshot()
        operation = self._operation_duration.snapshot()
        return {
            "event_counts": dict(self._events),
            "operation_outcomes": dict(self._outcomes),
            "preparation_outcomes": dict(self._preparations),
            "local_commits": {
                "new_receipt": self._commits["new_receipt"],
                "replayed_receipt": self._commits["replayed_receipt"],
            },
            "renewal_failures": {"local": dict(self._renewals["local"]), "platform": dict(self._renewals["platform"])},
            "unknown_commits_by_stage": dict(self._unknowns),
            "preparation_duration": preparation,
            "operation_duration": operation,
            "saturated": self._saturated or preparation["saturated"] or operation["saturated"],
        }
