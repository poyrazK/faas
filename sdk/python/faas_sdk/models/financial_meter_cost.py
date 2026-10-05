from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_allocation import FinancialAllocation
    from ..models.financial_price import FinancialPrice


T = TypeVar("T", bound="FinancialMeterCost")


@_attrs_define
class FinancialMeterCost:
    """Meter cost priced by one immutable contract with its assigned allowance."""

    price: FinancialPrice
    """Immutable version of a meter's exact price and allowance terms."""
    quantity: int
    included_quantity: int
    gross_millicents: int
    allowance_millicents: int
    net_millicents: int
    allocation_method: str
    allocations: list[FinancialAllocation]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        price = self.price.to_dict()

        quantity = self.quantity

        included_quantity = self.included_quantity

        gross_millicents = self.gross_millicents

        allowance_millicents = self.allowance_millicents

        net_millicents = self.net_millicents

        allocation_method = self.allocation_method

        allocations = []
        for allocations_item_data in self.allocations:
            allocations_item = allocations_item_data.to_dict()
            allocations.append(allocations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "price": price,
                "quantity": quantity,
                "included_quantity": included_quantity,
                "gross_millicents": gross_millicents,
                "allowance_millicents": allowance_millicents,
                "net_millicents": net_millicents,
                "allocation_method": allocation_method,
                "allocations": allocations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_allocation import FinancialAllocation
        from ..models.financial_price import FinancialPrice

        d = dict(src_dict)
        price = FinancialPrice.from_dict(d.pop("price"))

        quantity = d.pop("quantity")

        included_quantity = d.pop("included_quantity")

        gross_millicents = d.pop("gross_millicents")

        allowance_millicents = d.pop("allowance_millicents")

        net_millicents = d.pop("net_millicents")

        allocation_method = d.pop("allocation_method")

        allocations = []
        _allocations = d.pop("allocations")
        for allocations_item_data in _allocations:
            allocations_item = FinancialAllocation.from_dict(allocations_item_data)

            allocations.append(allocations_item)

        financial_meter_cost = cls(
            price=price,
            quantity=quantity,
            included_quantity=included_quantity,
            gross_millicents=gross_millicents,
            allowance_millicents=allowance_millicents,
            net_millicents=net_millicents,
            allocation_method=allocation_method,
            allocations=allocations,
        )

        financial_meter_cost.additional_properties = d
        return financial_meter_cost

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
