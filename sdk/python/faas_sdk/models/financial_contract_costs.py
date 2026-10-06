from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_meter_cost import FinancialMeterCost


T = TypeVar("T", bound="FinancialContractCosts")


@_attrs_define
class FinancialContractCosts:
    """One shared period allowance applied across historical rate versions."""

    meter: str
    quantity: int
    included_quantity: int
    net_millicents: int
    allowance_method: str
    contracts: list[FinancialMeterCost]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        meter = self.meter

        quantity = self.quantity

        included_quantity = self.included_quantity

        net_millicents = self.net_millicents

        allowance_method = self.allowance_method

        contracts = []
        for contracts_item_data in self.contracts:
            contracts_item = contracts_item_data.to_dict()
            contracts.append(contracts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "meter": meter,
                "quantity": quantity,
                "included_quantity": included_quantity,
                "net_millicents": net_millicents,
                "allowance_method": allowance_method,
                "contracts": contracts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_meter_cost import FinancialMeterCost

        d = dict(src_dict)
        meter = d.pop("meter")

        quantity = d.pop("quantity")

        included_quantity = d.pop("included_quantity")

        net_millicents = d.pop("net_millicents")

        allowance_method = d.pop("allowance_method")

        contracts = []
        _contracts = d.pop("contracts")
        for contracts_item_data in _contracts:
            contracts_item = FinancialMeterCost.from_dict(contracts_item_data)

            contracts.append(contracts_item)

        financial_contract_costs = cls(
            meter=meter,
            quantity=quantity,
            included_quantity=included_quantity,
            net_millicents=net_millicents,
            allowance_method=allowance_method,
            contracts=contracts,
        )

        financial_contract_costs.additional_properties = d
        return financial_contract_costs

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
