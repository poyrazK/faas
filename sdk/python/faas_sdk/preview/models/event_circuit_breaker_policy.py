from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventCircuitBreakerPolicy")


@_attrs_define
class EventCircuitBreakerPolicy:
    """Opt-in routing circuit breaker. Omitted properties use defaults. Failures and successful admissions form the sample;
    capacity waits and filtered events are excluded. Retained history must cover the observation window.

    """

    failure_threshold_pct: float | Unset = 50.0
    min_samples: int | Unset = 20
    window_seconds: int | Unset = 300
    cooldown_seconds: int | Unset = 60
    probe_successes: int | Unset = 3
    recovery_max_rate_per_second: int | Unset = 10
    recovery_seconds: int | Unset = 60

    def to_dict(self) -> dict[str, Any]:
        failure_threshold_pct = self.failure_threshold_pct

        min_samples = self.min_samples

        window_seconds = self.window_seconds

        cooldown_seconds = self.cooldown_seconds

        probe_successes = self.probe_successes

        recovery_max_rate_per_second = self.recovery_max_rate_per_second

        recovery_seconds = self.recovery_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if failure_threshold_pct is not UNSET:
            field_dict["failure_threshold_pct"] = failure_threshold_pct
        if min_samples is not UNSET:
            field_dict["min_samples"] = min_samples
        if window_seconds is not UNSET:
            field_dict["window_seconds"] = window_seconds
        if cooldown_seconds is not UNSET:
            field_dict["cooldown_seconds"] = cooldown_seconds
        if probe_successes is not UNSET:
            field_dict["probe_successes"] = probe_successes
        if recovery_max_rate_per_second is not UNSET:
            field_dict["recovery_max_rate_per_second"] = recovery_max_rate_per_second
        if recovery_seconds is not UNSET:
            field_dict["recovery_seconds"] = recovery_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        failure_threshold_pct = d.pop("failure_threshold_pct", UNSET)

        min_samples = d.pop("min_samples", UNSET)

        window_seconds = d.pop("window_seconds", UNSET)

        cooldown_seconds = d.pop("cooldown_seconds", UNSET)

        probe_successes = d.pop("probe_successes", UNSET)

        recovery_max_rate_per_second = d.pop("recovery_max_rate_per_second", UNSET)

        recovery_seconds = d.pop("recovery_seconds", UNSET)

        event_circuit_breaker_policy = cls(
            failure_threshold_pct=failure_threshold_pct,
            min_samples=min_samples,
            window_seconds=window_seconds,
            cooldown_seconds=cooldown_seconds,
            probe_successes=probe_successes,
            recovery_max_rate_per_second=recovery_max_rate_per_second,
            recovery_seconds=recovery_seconds,
        )

        return event_circuit_breaker_policy
