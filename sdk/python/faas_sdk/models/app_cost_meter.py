from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_cost_coverage import AppCostCoverage
    from ..models.financial_app_cost_allocation import FinancialAppCostAllocation


T = TypeVar("T", bound="AppCostMeter")


@_attrs_define
class AppCostMeter:
    """Exact retained quantity and cost allocations for one app and meter."""

    meter: str
    coverage: AppCostCoverage
    """Source sampling windows with per-app quantity coverage explicitly unknown."""
    quantity: int
    net_millicents: int
    allocations: list[FinancialAppCostAllocation]
    unit: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        meter = self.meter

        coverage = self.coverage.to_dict()

        quantity = self.quantity

        net_millicents = self.net_millicents

        allocations = []
        for allocations_item_data in self.allocations:
            allocations_item = allocations_item_data.to_dict()
            allocations.append(allocations_item)

        unit = self.unit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "meter": meter,
                "coverage": coverage,
                "quantity": quantity,
                "net_millicents": net_millicents,
                "allocations": allocations,
            }
        )
        if unit is not UNSET:
            field_dict["unit"] = unit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_cost_coverage import AppCostCoverage
        from ..models.financial_app_cost_allocation import FinancialAppCostAllocation

        d = dict(src_dict)
        meter = d.pop("meter")

        coverage = AppCostCoverage.from_dict(d.pop("coverage"))

        quantity = d.pop("quantity")

        net_millicents = d.pop("net_millicents")

        allocations = []
        _allocations = d.pop("allocations")
        for allocations_item_data in _allocations:
            allocations_item = FinancialAppCostAllocation.from_dict(allocations_item_data)

            allocations.append(allocations_item)

        unit = d.pop("unit", UNSET)

        app_cost_meter = cls(
            meter=meter,
            coverage=coverage,
            quantity=quantity,
            net_millicents=net_millicents,
            allocations=allocations,
            unit=unit,
        )

        app_cost_meter.additional_properties = d
        return app_cost_meter

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
