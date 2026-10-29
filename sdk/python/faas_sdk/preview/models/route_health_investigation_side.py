from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_health_investigation_example import RouteHealthInvestigationExample


T = TypeVar("T", bound="RouteHealthInvestigationSide")


@_attrs_define
class RouteHealthInvestigationSide:
    """Matching response weights and row inventory for one deployment/window, counted before the example cap. Error
    examples prioritize trace-linked rows, then newest timestamp and descending telemetry UUID. Latency examples
    prioritize the slowest latency bucket, then those same ties.

    """

    matching_requests: int
    observed_rows: int
    examples_truncated: bool
    examples: list[RouteHealthInvestigationExample]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        matching_requests = self.matching_requests

        observed_rows = self.observed_rows

        examples_truncated = self.examples_truncated

        examples = []
        for examples_item_data in self.examples:
            examples_item = examples_item_data.to_dict()
            examples.append(examples_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "matching_requests": matching_requests,
                "observed_rows": observed_rows,
                "examples_truncated": examples_truncated,
                "examples": examples,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_investigation_example import RouteHealthInvestigationExample

        d = dict(src_dict)
        matching_requests = d.pop("matching_requests")

        observed_rows = d.pop("observed_rows")

        examples_truncated = d.pop("examples_truncated")

        examples = []
        _examples = d.pop("examples")
        for examples_item_data in _examples:
            examples_item = RouteHealthInvestigationExample.from_dict(examples_item_data)

            examples.append(examples_item)

        route_health_investigation_side = cls(
            matching_requests=matching_requests,
            observed_rows=observed_rows,
            examples_truncated=examples_truncated,
            examples=examples,
        )

        route_health_investigation_side.additional_properties = d
        return route_health_investigation_side

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
