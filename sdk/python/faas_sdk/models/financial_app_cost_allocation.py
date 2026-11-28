from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_attribution import FinancialAttribution


T = TypeVar("T", bound="FinancialAppCostAllocation")


@_attrs_define
class FinancialAppCostAllocation:
    """Workload share of a meter cost attributed to one application."""

    attribution: FinancialAttribution
    """Workload identity retained at first observation, surviving rename and deletion."""
    unit: str
    quantity: int
    net_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attribution = self.attribution.to_dict()

        unit = self.unit

        quantity = self.quantity

        net_millicents = self.net_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attribution": attribution,
                "unit": unit,
                "quantity": quantity,
                "net_millicents": net_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_attribution import FinancialAttribution

        d = dict(src_dict)
        attribution = FinancialAttribution.from_dict(d.pop("attribution"))

        unit = d.pop("unit")

        quantity = d.pop("quantity")

        net_millicents = d.pop("net_millicents")

        financial_app_cost_allocation = cls(
            attribution=attribution,
            unit=unit,
            quantity=quantity,
            net_millicents=net_millicents,
        )

        financial_app_cost_allocation.additional_properties = d
        return financial_app_cost_allocation

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
