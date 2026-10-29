from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_investigation_example import RouteHealthInvestigationExample


T = TypeVar("T", bound="RouteHealthDependencyTiming")


@_attrs_define
class RouteHealthDependencyTiming:
    """Weighted nearest-rank p95 of retained spans. Calls use collapsed request weights and can exceed request counts with
    multiple spans. Exclusive p95 subtracts overlapping direct children; omitted if timing is incomplete or spans are
    capped. Examples are bounded request references.

    """

    span_samples: int
    represented_calls: int
    error_calls: int
    examples: list[RouteHealthInvestigationExample]
    p95_ms: int | Unset = UNSET
    exclusive_p95_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        span_samples = self.span_samples

        represented_calls = self.represented_calls

        error_calls = self.error_calls

        examples = []
        for examples_item_data in self.examples:
            examples_item = examples_item_data.to_dict()
            examples.append(examples_item)

        p95_ms = self.p95_ms

        exclusive_p95_ms = self.exclusive_p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "span_samples": span_samples,
                "represented_calls": represented_calls,
                "error_calls": error_calls,
                "examples": examples,
            }
        )
        if p95_ms is not UNSET:
            field_dict["p95_ms"] = p95_ms
        if exclusive_p95_ms is not UNSET:
            field_dict["exclusive_p95_ms"] = exclusive_p95_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_investigation_example import RouteHealthInvestigationExample

        d = dict(src_dict)
        span_samples = d.pop("span_samples")

        represented_calls = d.pop("represented_calls")

        error_calls = d.pop("error_calls")

        examples = []
        _examples = d.pop("examples")
        for examples_item_data in _examples:
            examples_item = RouteHealthInvestigationExample.from_dict(examples_item_data)

            examples.append(examples_item)

        p95_ms = d.pop("p95_ms", UNSET)

        exclusive_p95_ms = d.pop("exclusive_p95_ms", UNSET)

        route_health_dependency_timing = cls(
            span_samples=span_samples,
            represented_calls=represented_calls,
            error_calls=error_calls,
            examples=examples,
            p95_ms=p95_ms,
            exclusive_p95_ms=exclusive_p95_ms,
        )

        route_health_dependency_timing.additional_properties = d
        return route_health_dependency_timing

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
