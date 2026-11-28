from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugDependencyQueryItem")


@_attrs_define
class DebugDependencyQueryItem:
    """One normalized query pattern within a database dependency, with the same latency and failure comparison as its
    dependency.

    """

    pattern: str
    """Redacted, normalized statement; never contains literal values."""
    calls: int
    error_calls: int
    p95_ms: int
    regression: bool
    failure_regression: bool
    baseline_p95_ms: int | Unset = UNSET
    current_p95_ms: int | Unset = UNSET
    regression_factor: float | Unset = UNSET
    baseline_error_rate_pct: float | Unset = UNSET
    current_error_rate_pct: float | Unset = UNSET
    top_error_type: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pattern = self.pattern

        calls = self.calls

        error_calls = self.error_calls

        p95_ms = self.p95_ms

        regression = self.regression

        failure_regression = self.failure_regression

        baseline_p95_ms = self.baseline_p95_ms

        current_p95_ms = self.current_p95_ms

        regression_factor = self.regression_factor

        baseline_error_rate_pct = self.baseline_error_rate_pct

        current_error_rate_pct = self.current_error_rate_pct

        top_error_type = self.top_error_type

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pattern": pattern,
                "calls": calls,
                "error_calls": error_calls,
                "p95_ms": p95_ms,
                "regression": regression,
                "failure_regression": failure_regression,
            }
        )
        if baseline_p95_ms is not UNSET:
            field_dict["baseline_p95_ms"] = baseline_p95_ms
        if current_p95_ms is not UNSET:
            field_dict["current_p95_ms"] = current_p95_ms
        if regression_factor is not UNSET:
            field_dict["regression_factor"] = regression_factor
        if baseline_error_rate_pct is not UNSET:
            field_dict["baseline_error_rate_pct"] = baseline_error_rate_pct
        if current_error_rate_pct is not UNSET:
            field_dict["current_error_rate_pct"] = current_error_rate_pct
        if top_error_type is not UNSET:
            field_dict["top_error_type"] = top_error_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pattern = d.pop("pattern")

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        p95_ms = d.pop("p95_ms")

        regression = d.pop("regression")

        failure_regression = d.pop("failure_regression")

        baseline_p95_ms = d.pop("baseline_p95_ms", UNSET)

        current_p95_ms = d.pop("current_p95_ms", UNSET)

        regression_factor = d.pop("regression_factor", UNSET)

        baseline_error_rate_pct = d.pop("baseline_error_rate_pct", UNSET)

        current_error_rate_pct = d.pop("current_error_rate_pct", UNSET)

        top_error_type = d.pop("top_error_type", UNSET)

        debug_dependency_query_item = cls(
            pattern=pattern,
            calls=calls,
            error_calls=error_calls,
            p95_ms=p95_ms,
            regression=regression,
            failure_regression=failure_regression,
            baseline_p95_ms=baseline_p95_ms,
            current_p95_ms=current_p95_ms,
            regression_factor=regression_factor,
            baseline_error_rate_pct=baseline_error_rate_pct,
            current_error_rate_pct=current_error_rate_pct,
            top_error_type=top_error_type,
        )

        debug_dependency_query_item.additional_properties = d
        return debug_dependency_query_item

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
