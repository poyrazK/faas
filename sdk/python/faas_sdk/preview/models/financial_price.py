from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_price_currency import FinancialPriceCurrency, check_financial_price_currency

T = TypeVar("T", bound="FinancialPrice")


@_attrs_define
class FinancialPrice:
    """Immutable version of a meter's exact price and allowance terms."""

    version: str
    meter: str
    currency: FinancialPriceCurrency
    unit: str
    unit_quantity: int
    millicents_per_unit: int
    included_quantity: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        meter = self.meter

        currency: str = self.currency

        unit = self.unit

        unit_quantity = self.unit_quantity

        millicents_per_unit = self.millicents_per_unit

        included_quantity = self.included_quantity

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "meter": meter,
                "currency": currency,
                "unit": unit,
                "unit_quantity": unit_quantity,
                "millicents_per_unit": millicents_per_unit,
                "included_quantity": included_quantity,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        meter = d.pop("meter")

        currency = check_financial_price_currency(d.pop("currency"))

        unit = d.pop("unit")

        unit_quantity = d.pop("unit_quantity")

        millicents_per_unit = d.pop("millicents_per_unit")

        included_quantity = d.pop("included_quantity")

        financial_price = cls(
            version=version,
            meter=meter,
            currency=currency,
            unit=unit,
            unit_quantity=unit_quantity,
            millicents_per_unit=millicents_per_unit,
            included_quantity=included_quantity,
        )

        financial_price.additional_properties = d
        return financial_price

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
