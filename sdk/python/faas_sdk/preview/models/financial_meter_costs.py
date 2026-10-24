from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_contract_costs import FinancialContractCosts
    from ..models.financial_forecast import FinancialForecast
    from ..models.financial_meter_coverage import FinancialMeterCoverage
    from ..models.financial_price_contract import FinancialPriceContract


T = TypeVar("T", bound="FinancialMeterCosts")


@_attrs_define
class FinancialMeterCosts:
    """Accrued meter costs, source coverage, historical terms, and forecast."""

    meter: str
    coverage: FinancialMeterCoverage
    """Completeness and freshness of authoritative retained meter evidence."""
    accrued: FinancialContractCosts
    """One shared period allowance applied across historical rate versions."""
    forecast: FinancialForecast
    """Quantity run-rate projection; absent amounts mean unavailable, never zero."""
    price_contracts: list[FinancialPriceContract]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        meter = self.meter

        coverage = self.coverage.to_dict()

        accrued = self.accrued.to_dict()

        forecast = self.forecast.to_dict()

        price_contracts = []
        for price_contracts_item_data in self.price_contracts:
            price_contracts_item = price_contracts_item_data.to_dict()
            price_contracts.append(price_contracts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "meter": meter,
                "coverage": coverage,
                "accrued": accrued,
                "forecast": forecast,
                "price_contracts": price_contracts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_contract_costs import FinancialContractCosts
        from ..models.financial_forecast import FinancialForecast
        from ..models.financial_meter_coverage import FinancialMeterCoverage
        from ..models.financial_price_contract import FinancialPriceContract

        d = dict(src_dict)
        meter = d.pop("meter")

        coverage = FinancialMeterCoverage.from_dict(d.pop("coverage"))

        accrued = FinancialContractCosts.from_dict(d.pop("accrued"))

        forecast = FinancialForecast.from_dict(d.pop("forecast"))

        price_contracts = []
        _price_contracts = d.pop("price_contracts")
        for price_contracts_item_data in _price_contracts:
            price_contracts_item = FinancialPriceContract.from_dict(price_contracts_item_data)

            price_contracts.append(price_contracts_item)

        financial_meter_costs = cls(
            meter=meter,
            coverage=coverage,
            accrued=accrued,
            forecast=forecast,
            price_contracts=price_contracts,
        )

        financial_meter_costs.additional_properties = d
        return financial_meter_costs

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
