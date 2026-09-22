from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.egress_circuit_breaker_policy_state import (
    EgressCircuitBreakerPolicyState,
    check_egress_circuit_breaker_policy_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EgressCircuitBreakerPolicy")


@_attrs_define
class EgressCircuitBreakerPolicy:
    """Per-upstream egress circuit-breaker policy (ADR-201 §3).

    When enabled, your app's NEW connections to this upstream are
    rejected with a TCP reset while the circuit is open, instead of
    hanging for a full TCP connect timeout. That is the point of the
    feature: a blackholed dependency otherwise burns the per-request
    budget and then holds the wake slot on every request, turning one
    dependency outage into an app-wide capacity outage. Connections
    already established are never torn down.

    The circuit is driven by the platform's own 30-second TCP+TLS
    probe of the upstream, not by your traffic, so the half-open trial
    that decides recovery costs your app nothing.

    Disabled by default. This rule can cut an app off from its own
    database, so it is never inferred — you opt in per upstream.

    """

    enabled: bool = False
    """Whether egress breaking is active for this upstream."""
    failure_threshold: float | Unset = UNSET
    """Failure RATIO (not a count) at or above which the circuit
    opens. Omitted tracks the platform default (0.5), so the
    upstream follows the default as it evolves rather than
    freezing today's value.
    """
    min_samples: int | Unset = UNSET
    """Probe samples required inside the window before the ratio is
    consulted. Omitted tracks the platform default (3). The probe
    samples every 30s, so a high value can take a long time to
    accumulate — or never will on a short-lived app.
    """
    open_seconds: int | Unset = UNSET
    """First open interval before a half-open probe is offered.
    Omitted tracks the platform default (30). Each failed probe
    doubles the interval, up to the platform ceiling.
    """
    state: EgressCircuitBreakerPolicyState | Unset = UNSET
    """Observed circuit state. Absent when the breaker is disabled or
    the scheduler has not yet reported one.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        failure_threshold = self.failure_threshold

        min_samples = self.min_samples

        open_seconds = self.open_seconds

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if failure_threshold is not UNSET:
            field_dict["failure_threshold"] = failure_threshold
        if min_samples is not UNSET:
            field_dict["min_samples"] = min_samples
        if open_seconds is not UNSET:
            field_dict["open_seconds"] = open_seconds
        if state is not UNSET:
            field_dict["state"] = state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        failure_threshold = d.pop("failure_threshold", UNSET)

        min_samples = d.pop("min_samples", UNSET)

        open_seconds = d.pop("open_seconds", UNSET)

        _state = d.pop("state", UNSET)
        state: EgressCircuitBreakerPolicyState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_egress_circuit_breaker_policy_state(_state)

        egress_circuit_breaker_policy = cls(
            enabled=enabled,
            failure_threshold=failure_threshold,
            min_samples=min_samples,
            open_seconds=open_seconds,
            state=state,
        )

        egress_circuit_breaker_policy.additional_properties = d
        return egress_circuit_breaker_policy

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
