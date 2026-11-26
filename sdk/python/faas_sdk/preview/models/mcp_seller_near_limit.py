from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="MCPSellerNearLimit")


@_attrs_define
class MCPSellerNearLimit:
    """An open customer at 80% or more of its plan's monthly units this month."""

    consumer_id: str
    name: str
    plan: str
    units: int
    max_units_per_month: int
    percent: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = self.consumer_id

        name = self.name

        plan = self.plan

        units = self.units

        max_units_per_month = self.max_units_per_month

        percent = self.percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "name": name,
                "plan": plan,
                "units": units,
                "max_units_per_month": max_units_per_month,
                "percent": percent,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_id = d.pop("consumer_id")

        name = d.pop("name")

        plan = d.pop("plan")

        units = d.pop("units")

        max_units_per_month = d.pop("max_units_per_month")

        percent = d.pop("percent")

        mcp_seller_near_limit = cls(
            consumer_id=consumer_id,
            name=name,
            plan=plan,
            units=units,
            max_units_per_month=max_units_per_month,
            percent=percent,
        )

        mcp_seller_near_limit.additional_properties = d
        return mcp_seller_near_limit

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
