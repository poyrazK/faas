from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_dependency_comparison_status import (
    RouteHealthDependencyComparisonStatus,
    check_route_health_dependency_comparison_status,
)
from ..models.route_health_dependency_comparison_type import (
    RouteHealthDependencyComparisonType,
    check_route_health_dependency_comparison_type,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_dependency_timing import RouteHealthDependencyTiming


T = TypeVar("T", bound="RouteHealthDependencyComparison")


@_attrs_define
class RouteHealthDependencyComparison:
    """Retained dependency comparison. Deltas require measurements from both deployments. Ordered by positive comparable
    p95 change, candidate p95, type and kind; at most 16 groups.

    """

    type_: RouteHealthDependencyComparisonType
    status: RouteHealthDependencyComparisonStatus
    candidate: RouteHealthDependencyTiming
    """Weighted nearest-rank p95 of retained spans. Calls use collapsed request weights and can exceed request
    counts with multiple spans. Exclusive p95 subtracts overlapping direct children; omitted if timing is incomplete
    or spans are capped. Examples are bounded request references."""
    stable: RouteHealthDependencyTiming
    """Weighted nearest-rank p95 of retained spans. Calls use collapsed request weights and can exceed request
    counts with multiple spans. Exclusive p95 subtracts overlapping direct children; omitted if timing is incomplete
    or spans are capped. Examples are bounded request references."""
    kind: str | Unset = UNSET
    p95_delta_ms: int | Unset = UNSET
    exclusive_p95_delta_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        status: str = self.status

        candidate = self.candidate.to_dict()

        stable = self.stable.to_dict()

        kind = self.kind

        p95_delta_ms = self.p95_delta_ms

        exclusive_p95_delta_ms = self.exclusive_p95_delta_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "status": status,
                "candidate": candidate,
                "stable": stable,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if p95_delta_ms is not UNSET:
            field_dict["p95_delta_ms"] = p95_delta_ms
        if exclusive_p95_delta_ms is not UNSET:
            field_dict["exclusive_p95_delta_ms"] = exclusive_p95_delta_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_dependency_timing import RouteHealthDependencyTiming

        d = dict(src_dict)
        type_ = check_route_health_dependency_comparison_type(d.pop("type"))

        status = check_route_health_dependency_comparison_status(d.pop("status"))

        candidate = RouteHealthDependencyTiming.from_dict(d.pop("candidate"))

        stable = RouteHealthDependencyTiming.from_dict(d.pop("stable"))

        kind = d.pop("kind", UNSET)

        p95_delta_ms = d.pop("p95_delta_ms", UNSET)

        exclusive_p95_delta_ms = d.pop("exclusive_p95_delta_ms", UNSET)

        route_health_dependency_comparison = cls(
            type_=type_,
            status=status,
            candidate=candidate,
            stable=stable,
            kind=kind,
            p95_delta_ms=p95_delta_ms,
            exclusive_p95_delta_ms=exclusive_p95_delta_ms,
        )

        route_health_dependency_comparison.additional_properties = d
        return route_health_dependency_comparison

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
