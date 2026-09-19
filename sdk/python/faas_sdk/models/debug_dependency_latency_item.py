from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_dependency_latency_item_type import (
    DebugDependencyLatencyItemType,
    check_debug_dependency_latency_item_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugDependencyLatencyItem")


@_attrs_define
class DebugDependencyLatencyItem:
    """Bounded historical dependency aggregate. Percentiles are weighted by collapsed request-row counts and derived from
    sampled redacted spans.

    """

    type_: DebugDependencyLatencyItemType
    name: str
    calls: int
    error_calls: int
    error_rate_pct: float
    p50_ms: int
    p95_ms: int
    p99_ms: int
    regression: bool
    """True when both split-window samples meet the minimum call threshold and current p95 is at least 1.5x and
    25ms above baseline."""
    kind: str | Unset = UNSET
    baseline_p95_ms: int | Unset = UNSET
    current_p95_ms: int | Unset = UNSET
    p95_delta_ms: int | Unset = UNSET
    regression_factor: float | Unset = UNSET
    baseline_error_rate_pct: float | Unset = UNSET
    current_error_rate_pct: float | Unset = UNSET
    error_rate_delta_pct: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        name = self.name

        calls = self.calls

        error_calls = self.error_calls

        error_rate_pct = self.error_rate_pct

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        regression = self.regression

        kind = self.kind

        baseline_p95_ms = self.baseline_p95_ms

        current_p95_ms = self.current_p95_ms

        p95_delta_ms = self.p95_delta_ms

        regression_factor = self.regression_factor

        baseline_error_rate_pct = self.baseline_error_rate_pct

        current_error_rate_pct = self.current_error_rate_pct

        error_rate_delta_pct = self.error_rate_delta_pct

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "calls": calls,
                "error_calls": error_calls,
                "error_rate_pct": error_rate_pct,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "regression": regression,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if baseline_p95_ms is not UNSET:
            field_dict["baseline_p95_ms"] = baseline_p95_ms
        if current_p95_ms is not UNSET:
            field_dict["current_p95_ms"] = current_p95_ms
        if p95_delta_ms is not UNSET:
            field_dict["p95_delta_ms"] = p95_delta_ms
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
        d = dict(src_dict)
        type_ = check_debug_dependency_latency_item_type(d.pop("type"))

        name = d.pop("name")

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        error_rate_pct = d.pop("error_rate_pct")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        regression = d.pop("regression")

        kind = d.pop("kind", UNSET)

        baseline_p95_ms = d.pop("baseline_p95_ms", UNSET)

        current_p95_ms = d.pop("current_p95_ms", UNSET)

        p95_delta_ms = d.pop("p95_delta_ms", UNSET)

        regression_factor = d.pop("regression_factor", UNSET)

        baseline_error_rate_pct = d.pop("baseline_error_rate_pct", UNSET)

        current_error_rate_pct = d.pop("current_error_rate_pct", UNSET)

        error_rate_delta_pct = d.pop("error_rate_delta_pct", UNSET)

        debug_dependency_latency_item = cls(
            type_=type_,
            name=name,
            calls=calls,
            error_calls=error_calls,
            error_rate_pct=error_rate_pct,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            regression=regression,
            kind=kind,
            baseline_p95_ms=baseline_p95_ms,
            current_p95_ms=current_p95_ms,
            p95_delta_ms=p95_delta_ms,
            regression_factor=regression_factor,
            baseline_error_rate_pct=baseline_error_rate_pct,
            current_error_rate_pct=current_error_rate_pct,
            error_rate_delta_pct=error_rate_delta_pct,
        )

        debug_dependency_latency_item.additional_properties = d
        return debug_dependency_latency_item

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
