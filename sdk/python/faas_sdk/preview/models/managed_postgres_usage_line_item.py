from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_usage_line_item_code import (
    ManagedPostgresUsageLineItemCode,
    check_managed_postgres_usage_line_item_code,
)

T = TypeVar("T", bound="ManagedPostgresUsageLineItem")


@_attrs_define
class ManagedPostgresUsageLineItem:
    """Normalized operator-only managed PostgreSQL ledger line."""

    code: ManagedPostgresUsageLineItemCode
    meter: str
    unit: str
    quantity: int
    cost_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        meter = self.meter

        unit = self.unit

        quantity = self.quantity

        cost_millicents = self.cost_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "meter": meter,
                "unit": unit,
                "quantity": quantity,
                "cost_millicents": cost_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = check_managed_postgres_usage_line_item_code(d.pop("code"))

        meter = d.pop("meter")

        unit = d.pop("unit")

        quantity = d.pop("quantity")

        cost_millicents = d.pop("cost_millicents")

        managed_postgres_usage_line_item = cls(
            code=code,
            meter=meter,
            unit=unit,
            quantity=quantity,
            cost_millicents=cost_millicents,
        )

        managed_postgres_usage_line_item.additional_properties = d
        return managed_postgres_usage_line_item

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
