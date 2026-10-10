from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_suspected_dependency_reason import (
    DebugSuspectedDependencyReason,
    check_debug_suspected_dependency_reason,
)
from ..models.debug_suspected_dependency_type import DebugSuspectedDependencyType, check_debug_suspected_dependency_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugSuspectedDependency")


@_attrs_define
class DebugSuspectedDependency:
    """The classified dependency most likely behind this route regression (ADR-934): the one whose p95 regressed most
    between the previous and the regressed deployment, else one that started failing more often. Bounded and redacted;
    also sent in debug.regression.* webhooks.

    """

    type_: DebugSuspectedDependencyType
    name: str
    p95_base_ms: int
    p95_ms: int
    regression_factor: float
    kind: str | Unset = UNSET
    reason: DebugSuspectedDependencyReason | Unset = UNSET
    """latency when the dependency's p95 regressed; failures when its error rate rose. Absent on suspects recorded
    before failure attribution, which are latency suspects."""
    baseline_error_rate_pct: float | Unset = UNSET
    error_rate_pct: float | Unset = UNSET
    error_type: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        name = self.name

        p95_base_ms = self.p95_base_ms

        p95_ms = self.p95_ms

        regression_factor = self.regression_factor

        kind = self.kind

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        baseline_error_rate_pct = self.baseline_error_rate_pct

        error_rate_pct = self.error_rate_pct

        error_type = self.error_type

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "p95_base_ms": p95_base_ms,
                "p95_ms": p95_ms,
                "regression_factor": regression_factor,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if reason is not UNSET:
            field_dict["reason"] = reason
        if baseline_error_rate_pct is not UNSET:
            field_dict["baseline_error_rate_pct"] = baseline_error_rate_pct
        if error_rate_pct is not UNSET:
            field_dict["error_rate_pct"] = error_rate_pct
        if error_type is not UNSET:
            field_dict["error_type"] = error_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_debug_suspected_dependency_type(d.pop("type"))

        name = d.pop("name")

        p95_base_ms = d.pop("p95_base_ms")

        p95_ms = d.pop("p95_ms")

        regression_factor = d.pop("regression_factor")

        kind = d.pop("kind", UNSET)

        _reason = d.pop("reason", UNSET)
        reason: DebugSuspectedDependencyReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_debug_suspected_dependency_reason(_reason)

        baseline_error_rate_pct = d.pop("baseline_error_rate_pct", UNSET)

        error_rate_pct = d.pop("error_rate_pct", UNSET)

        error_type = d.pop("error_type", UNSET)

        debug_suspected_dependency = cls(
            type_=type_,
            name=name,
            p95_base_ms=p95_base_ms,
            p95_ms=p95_ms,
            regression_factor=regression_factor,
            kind=kind,
            reason=reason,
            baseline_error_rate_pct=baseline_error_rate_pct,
            error_rate_pct=error_rate_pct,
            error_type=error_type,
        )

        debug_suspected_dependency.additional_properties = d
        return debug_suspected_dependency

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
