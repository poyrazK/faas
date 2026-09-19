from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_critical_path_segment import DebugCriticalPathSegment


T = TypeVar("T", bound="DebugDependencyImpactEdge")


@_attrs_define
class DebugDependencyImpactEdge:
    """Bounded normalized parent-to-child dependency edge reconstructed from retained redacted span links."""

    from_: DebugCriticalPathSegment
    """One redacted span identity in a canonical historical critical-path signature."""
    to: DebugCriticalPathSegment
    """One redacted span identity in a canonical historical critical-path signature."""
    calls: int
    error_calls: int
    error_rate_pct: float
    p50_ms: int
    p95_ms: int
    p99_ms: int
    exclusive_p50_ms: int
    exclusive_p95_ms: int
    exclusive_p99_ms: int
    regression: bool
    baseline_p95_ms: int | Unset = UNSET
    current_p95_ms: int | Unset = UNSET
    p95_delta_ms: int | Unset = UNSET
    baseline_exclusive_p95_ms: int | Unset = UNSET
    current_exclusive_p95_ms: int | Unset = UNSET
    exclusive_p95_delta_ms: int | Unset = UNSET
    regression_factor: float | Unset = UNSET
    baseline_error_rate_pct: float | Unset = UNSET
    current_error_rate_pct: float | Unset = UNSET
    error_rate_delta_pct: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_.to_dict()

        to = self.to.to_dict()

        calls = self.calls

        error_calls = self.error_calls

        error_rate_pct = self.error_rate_pct

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        exclusive_p50_ms = self.exclusive_p50_ms

        exclusive_p95_ms = self.exclusive_p95_ms

        exclusive_p99_ms = self.exclusive_p99_ms

        regression = self.regression

        baseline_p95_ms = self.baseline_p95_ms

        current_p95_ms = self.current_p95_ms

        p95_delta_ms = self.p95_delta_ms

        baseline_exclusive_p95_ms = self.baseline_exclusive_p95_ms

        current_exclusive_p95_ms = self.current_exclusive_p95_ms

        exclusive_p95_delta_ms = self.exclusive_p95_delta_ms

        regression_factor = self.regression_factor

        baseline_error_rate_pct = self.baseline_error_rate_pct

        current_error_rate_pct = self.current_error_rate_pct

        error_rate_delta_pct = self.error_rate_delta_pct

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "from": from_,
                "to": to,
                "calls": calls,
                "error_calls": error_calls,
                "error_rate_pct": error_rate_pct,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "exclusive_p50_ms": exclusive_p50_ms,
                "exclusive_p95_ms": exclusive_p95_ms,
                "exclusive_p99_ms": exclusive_p99_ms,
                "regression": regression,
            }
        )
        if baseline_p95_ms is not UNSET:
            field_dict["baseline_p95_ms"] = baseline_p95_ms
        if current_p95_ms is not UNSET:
            field_dict["current_p95_ms"] = current_p95_ms
        if p95_delta_ms is not UNSET:
            field_dict["p95_delta_ms"] = p95_delta_ms
        if baseline_exclusive_p95_ms is not UNSET:
            field_dict["baseline_exclusive_p95_ms"] = baseline_exclusive_p95_ms
        if current_exclusive_p95_ms is not UNSET:
            field_dict["current_exclusive_p95_ms"] = current_exclusive_p95_ms
        if exclusive_p95_delta_ms is not UNSET:
            field_dict["exclusive_p95_delta_ms"] = exclusive_p95_delta_ms
        if regression_factor is not UNSET:
            field_dict["regression_factor"] = regression_factor
        if baseline_error_rate_pct is not UNSET:
            field_dict["baseline_error_rate_pct"] = baseline_error_rate_pct
        if current_error_rate_pct is not UNSET:
            field_dict["current_error_rate_pct"] = current_error_rate_pct
        if error_rate_delta_pct is not UNSET:
            field_dict["error_rate_delta_pct"] = error_rate_delta_pct

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_critical_path_segment import DebugCriticalPathSegment

        d = dict(src_dict)
        from_ = DebugCriticalPathSegment.from_dict(d.pop("from"))

        to = DebugCriticalPathSegment.from_dict(d.pop("to"))

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        error_rate_pct = d.pop("error_rate_pct")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        exclusive_p50_ms = d.pop("exclusive_p50_ms")

        exclusive_p95_ms = d.pop("exclusive_p95_ms")

        exclusive_p99_ms = d.pop("exclusive_p99_ms")

        regression = d.pop("regression")

        baseline_p95_ms = d.pop("baseline_p95_ms", UNSET)

        current_p95_ms = d.pop("current_p95_ms", UNSET)

        p95_delta_ms = d.pop("p95_delta_ms", UNSET)

        baseline_exclusive_p95_ms = d.pop("baseline_exclusive_p95_ms", UNSET)

        current_exclusive_p95_ms = d.pop("current_exclusive_p95_ms", UNSET)

        exclusive_p95_delta_ms = d.pop("exclusive_p95_delta_ms", UNSET)

        regression_factor = d.pop("regression_factor", UNSET)

        baseline_error_rate_pct = d.pop("baseline_error_rate_pct", UNSET)

        current_error_rate_pct = d.pop("current_error_rate_pct", UNSET)

        error_rate_delta_pct = d.pop("error_rate_delta_pct", UNSET)

        debug_dependency_impact_edge = cls(
            from_=from_,
            to=to,
            calls=calls,
            error_calls=error_calls,
            error_rate_pct=error_rate_pct,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            exclusive_p50_ms=exclusive_p50_ms,
            exclusive_p95_ms=exclusive_p95_ms,
            exclusive_p99_ms=exclusive_p99_ms,
            regression=regression,
            baseline_p95_ms=baseline_p95_ms,
            current_p95_ms=current_p95_ms,
            p95_delta_ms=p95_delta_ms,
            baseline_exclusive_p95_ms=baseline_exclusive_p95_ms,
            current_exclusive_p95_ms=current_exclusive_p95_ms,
            exclusive_p95_delta_ms=exclusive_p95_delta_ms,
            regression_factor=regression_factor,
            baseline_error_rate_pct=baseline_error_rate_pct,
            current_error_rate_pct=current_error_rate_pct,
            error_rate_delta_pct=error_rate_delta_pct,
        )

        debug_dependency_impact_edge.additional_properties = d
        return debug_dependency_impact_edge

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
