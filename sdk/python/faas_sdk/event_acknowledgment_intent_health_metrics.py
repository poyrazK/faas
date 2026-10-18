"""One owner-scoped cached observation. Snapshotting performs no database I/O."""

from __future__ import annotations

import math
import re
import time
from dataclasses import dataclass, replace
from typing import Any, NoReturn

from ._event_deadline_policy import timestamp
from .event_acknowledgment_intent_health import EventAcknowledgmentIntentHealth
from .event_consumer_transactions import _uuid

_INVALID = "invalid acknowledgment recovery health metrics input"
_MAX_NUMBER = 9007199254740991
_NANOSECONDS = 1000000000


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentHealthMetricsOptions:
    max_age_ms: int = 30000


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentHealthMetricsSample:
    observed_at: str
    observation_age_seconds: float
    complete: bool
    truncated: bool
    scan_limit: int
    invalid_rows: int
    outstanding: int
    due: int
    stalled: int
    parked: int


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentHealthMetricsSnapshot:
    read_success: bool
    evidence_eligible: bool
    max_observation_age_seconds: float
    health: EventAcknowledgmentIntentHealthMetricsSample | None = None


def _invalid() -> NoReturn:
    raise ValueError(_INVALID)


def _count(value: Any, maximum: int) -> int:
    if type(value) is not int or not 0 <= value <= maximum:
        _invalid()
    return value


def _flag(value: Any) -> bool:
    if type(value) is not bool:
        _invalid()
    return value


def _age(value: Any) -> float:
    if type(value) not in (int, float) or not 0 <= value <= _MAX_NUMBER or not math.isfinite(value):
        _invalid()
    return float(value)


def _instant_ns(value: Any) -> int:
    if (
        type(value) is not str
        or re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z", value) is None
    ):
        _invalid()
    at = timestamp(value)
    if at is None:
        _invalid()
    return at


def _sample(value: EventAcknowledgmentIntentHealthMetricsSample) -> EventAcknowledgmentIntentHealthMetricsSample:
    if type(value) is not EventAcknowledgmentIntentHealthMetricsSample:
        _invalid()
    limit = _count(value.scan_limit, 10000)
    complete, truncated = _flag(value.complete), _flag(value.truncated)
    invalid = _count(value.invalid_rows, limit)
    outstanding, due, stalled, parked = (
        _count(n, limit) for n in (value.outstanding, value.due, value.stalled, value.parked)
    )
    _instant_ns(value.observed_at)
    age = _age(value.observation_age_seconds)
    if (
        limit == 0
        or complete != (not truncated and invalid == 0)
        or invalid > outstanding
        or due + parked > outstanding
        or stalled > due
        or truncated
        and outstanding != limit
    ):
        _invalid()
    return EventAcknowledgmentIntentHealthMetricsSample(
        value.observed_at, age, complete, truncated, limit, invalid, outstanding, due, stalled, parked
    )


def _copy_health(
    value: EventAcknowledgmentIntentHealth, account: str, app: str
) -> EventAcknowledgmentIntentHealthMetricsSample:
    if type(value) is not EventAcknowledgmentIntentHealth:
        _invalid()
    if (
        type(value.account_id) is not str
        or type(value.app_id) is not str
        or _uuid(value.account_id) != account
        or _uuid(value.app_id) != app
        or type(value.coverage) is not str
        or value.coverage != "bounded_retained_unfinished_intents"
    ):
        _invalid()
    out = _sample(
        EventAcknowledgmentIntentHealthMetricsSample(
            value.observed_at,
            0.0,
            value.complete,
            value.truncated,
            value.scan_limit,
            value.invalid_rows,
            value.outstanding,
            value.due,
            value.stalled,
            value.parked,
        )
    )
    pending, unconfirmed, leased, scheduled, expired = (
        _count(n, out.scan_limit)
        for n in (value.pending, value.unconfirmed, value.leased, value.scheduled, value.expired_claims)
    )
    if (
        pending + unconfirmed + out.parked != out.outstanding
        or out.due + leased + scheduled + out.parked != out.outstanding
        or expired > out.due + scheduled
    ):
        _invalid()
    return out


@dataclass(frozen=True, slots=True)
class _Recorded:
    health: EventAcknowledgmentIntentHealthMetricsSample
    observed_ns: int
    wall_ns: int
    local_ns: int
    age_ns: int
    eligible: bool


class EventAcknowledgmentIntentHealthMetrics:
    """Fixed-memory cache; one serialized owner/poller. Importing starts no work.

    Malformed/failed reads clear cached counts and retain the timestamp high-water
    mark. Snapshots expose no owner identity, capability, payload or raw error.
    """

    __slots__ = ("__account", "__app", "__max_age_ns", "__previous_observed", "__sample", "__polling")

    def __init__(
        self, account_id: str, app_id: str, options: EventAcknowledgmentIntentHealthMetricsOptions | None = None
    ) -> None:
        copied = None
        try:
            if options is None:
                options = EventAcknowledgmentIntentHealthMetricsOptions()
            if type(options) is not EventAcknowledgmentIntentHealthMetricsOptions:
                _invalid()
            if type(account_id) is not str or type(app_id) is not str:
                _invalid()
            maximum = _count(options.max_age_ms, 300000)
            if maximum < 100:
                _invalid()
            copied = (_uuid(account_id), _uuid(app_id), maximum * 1000000)
        except Exception:
            pass
        del account_id, app_id, options
        if copied is None:
            raise ValueError(_INVALID)
        self.__account, self.__app, self.__max_age_ns = copied
        self.__previous_observed: int | None = None
        self.__sample: _Recorded | None = None
        self.__polling = False

    def record(self, health: EventAcknowledgmentIntentHealth) -> None:
        copied = None
        try:
            copied = _copy_health(health, self.__account, self.__app)
        except Exception:
            pass
        del health
        if copied is None:
            self.record_read_failure()
            raise ValueError(_INVALID)
        observed = _instant_ns(copied.observed_at)
        wall, local = time.time_ns(), time.monotonic_ns()
        age = max(0, wall - observed)
        fresh = observed <= wall and age <= self.__max_age_ns
        advancing = self.__previous_observed is None or observed > self.__previous_observed
        self.__sample = _Recorded(copied, observed, wall, local, age, copied.complete and fresh and advancing)
        if fresh and advancing:
            self.__previous_observed = observed

    def record_read_failure(self) -> None:
        self.__sample = None

    def snapshot(self) -> EventAcknowledgmentIntentHealthMetricsSnapshot:
        saved, maximum = self.__sample, self.__max_age_ns / _NANOSECONDS
        if saved is None:
            return EventAcknowledgmentIntentHealthMetricsSnapshot(False, False, maximum)
        wall, local = time.time_ns(), time.monotonic_ns()
        elapsed = local - saved.local_ns
        age = max(0, wall - saved.observed_ns, saved.age_ns + max(0, elapsed))
        eligible = saved.eligible and wall >= saved.wall_ns and elapsed >= 0 and age <= self.__max_age_ns
        return EventAcknowledgmentIntentHealthMetricsSnapshot(
            True, eligible, maximum, replace(saved.health, observation_age_seconds=age / _NANOSECONDS)
        )

    def _begin_polling(self, account: str, app: str) -> None:
        if self.__polling or account != self.__account or app != self.__app:
            raise ValueError("recovery metrics require matching scope and one poller")
        self.__polling = True

    def _end_polling(self) -> None:
        self.record_read_failure()
        self.__polling = False


def _validated_snapshot(
    value: EventAcknowledgmentIntentHealthMetricsSnapshot,
) -> EventAcknowledgmentIntentHealthMetricsSnapshot:
    if type(value) is not EventAcknowledgmentIntentHealthMetricsSnapshot:
        _invalid()
    read, eligible, maximum = (
        _flag(value.read_success),
        _flag(value.evidence_eligible),
        _age(value.max_observation_age_seconds),
    )
    if not 0.1 <= maximum <= 300 or read != (value.health is not None):
        _invalid()
    health = None if value.health is None else _sample(value.health)
    if eligible and (health is None or not health.complete or health.observation_age_seconds > maximum):
        _invalid()
    return EventAcknowledgmentIntentHealthMetricsSnapshot(read, eligible, maximum, health)
