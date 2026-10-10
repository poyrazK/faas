from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UpdateAPIConsumerPlanLimitsRequest")


@_attrs_define
class UpdateAPIConsumerPlanLimitsRequest:
    """Replacement limits for a consumer plan."""

    max_requests_per_minute: int
    max_units_per_month: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_requests_per_minute = self.max_requests_per_minute

        max_units_per_month = self.max_units_per_month

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_requests_per_minute": max_requests_per_minute,
                "max_units_per_month": max_units_per_month,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_requests_per_minute = d.pop("max_requests_per_minute")

        max_units_per_month = d.pop("max_units_per_month")

        update_api_consumer_plan_limits_request = cls(
            max_requests_per_minute=max_requests_per_minute,
            max_units_per_month=max_units_per_month,
        )

        update_api_consumer_plan_limits_request.additional_properties = d
        return update_api_consumer_plan_limits_request

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
