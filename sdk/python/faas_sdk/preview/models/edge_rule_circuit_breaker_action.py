from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleCircuitBreakerAction")


@_attrs_define
class EdgeRuleCircuitBreakerAction:
    """Instance health thresholds (ADR-201 §2, kind=circuit_breaker).

    This rule TUNES a breaker that already runs for every app on
    every plan; it does not enable protection. With no rule your
    app is still protected from a flapping instance by the
    platform defaults below.

    States are closed → open → half_open. In half_open exactly one
    request is admitted as a probe: success closes the circuit and
    resets the backoff, failure returns it to open with the
    interval doubled, up to `max_open_seconds`.

    """

    failure_threshold: float | Unset = 0.5
    """Failure RATIO (not a count) at or above which a closed
    breaker opens. Consulted only once `min_requests`
    observations exist inside the window. 0 applies the
    default (0.5).
    """
    min_requests: int | Unset = 5
    """Minimum observations inside `window_seconds` before the
    ratio is consulted at all. 0 applies the default (5).

    This is the field most likely to be set badly. At 1 a
    single transport blip opens the circuit, which on a route
    serving one request a minute reads as a 100% failure rate.
    """
    window_seconds: int | Unset = 10
    """Rolling failure window. 0 applies the default (10s)."""
    open_seconds: int | Unset = 5
    """First open interval before a half-open probe is offered. 0
    applies the default (5s). Each failed probe doubles the
    interval up to `max_open_seconds`.
    """
    max_open_seconds: int | Unset = 60
    """Ceiling the exponential backoff grows toward. 0 applies the
    default (60s). Must be at least `open_seconds`. The cap is
    1 hour: a longer bench outlives most instances, so the
    breaker would be holding state about a target that no
    longer exists.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        failure_threshold = self.failure_threshold

        min_requests = self.min_requests

        window_seconds = self.window_seconds

        open_seconds = self.open_seconds

        max_open_seconds = self.max_open_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if failure_threshold is not UNSET:
            field_dict["failure_threshold"] = failure_threshold
        if min_requests is not UNSET:
            field_dict["min_requests"] = min_requests
        if window_seconds is not UNSET:
            field_dict["window_seconds"] = window_seconds
        if open_seconds is not UNSET:
            field_dict["open_seconds"] = open_seconds
        if max_open_seconds is not UNSET:
            field_dict["max_open_seconds"] = max_open_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        failure_threshold = d.pop("failure_threshold", UNSET)

        min_requests = d.pop("min_requests", UNSET)

        window_seconds = d.pop("window_seconds", UNSET)

        open_seconds = d.pop("open_seconds", UNSET)

        max_open_seconds = d.pop("max_open_seconds", UNSET)

        edge_rule_circuit_breaker_action = cls(
            failure_threshold=failure_threshold,
            min_requests=min_requests,
            window_seconds=window_seconds,
            open_seconds=open_seconds,
            max_open_seconds=max_open_seconds,
        )

        edge_rule_circuit_breaker_action.additional_properties = d
        return edge_rule_circuit_breaker_action

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
