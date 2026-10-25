from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_attribution import FinancialAttribution


T = TypeVar("T", bound="FinancialAllocation")


@_attrs_define
class FinancialAllocation:
    """Exact quantity-share allocation; row amounts and account totals reconcile."""

    attribution: FinancialAttribution
    """Workload identity retained at first observation, surviving rename and deletion."""
    quantity: int
    gross_millicents: int
    allowance_millicents: int
    net_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attribution = self.attribution.to_dict()

        quantity = self.quantity

        gross_millicents = self.gross_millicents

        allowance_millicents = self.allowance_millicents

        net_millicents = self.net_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attribution": attribution,
                "quantity": quantity,
                "gross_millicents": gross_millicents,
                "allowance_millicents": allowance_millicents,
                "net_millicents": net_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_attribution import FinancialAttribution

        d = dict(src_dict)
        attribution = FinancialAttribution.from_dict(d.pop("attribution"))

        quantity = d.pop("quantity")

        gross_millicents = d.pop("gross_millicents")

        allowance_millicents = d.pop("allowance_millicents")

        net_millicents = d.pop("net_millicents")

        financial_allocation = cls(
            attribution=attribution,
            quantity=quantity,
            gross_millicents=gross_millicents,
            allowance_millicents=allowance_millicents,
            net_millicents=net_millicents,
        )

        financial_allocation.additional_properties = d
        return financial_allocation

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
