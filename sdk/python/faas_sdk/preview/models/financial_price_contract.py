from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_price_contract_delivery_mode import (
    FinancialPriceContractDeliveryMode,
    check_financial_price_contract_delivery_mode,
)
from ..models.financial_price_contract_plan import FinancialPriceContractPlan, check_financial_price_contract_plan

if TYPE_CHECKING:
    from ..models.financial_price import FinancialPrice


T = TypeVar("T", bound="FinancialPriceContract")


@_attrs_define
class FinancialPriceContract:
    """Historical activation of recorded account pricing."""

    price: FinancialPrice
    """Immutable version of a meter's exact price and allowance terms."""
    plan: FinancialPriceContractPlan
    effective_from: datetime.datetime
    delivery_mode: FinancialPriceContractDeliveryMode
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        price = self.price.to_dict()

        plan: str = self.plan

        effective_from = self.effective_from.isoformat()

        delivery_mode: str = self.delivery_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "price": price,
                "plan": plan,
                "effective_from": effective_from,
                "delivery_mode": delivery_mode,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_price import FinancialPrice

        d = dict(src_dict)
        price = FinancialPrice.from_dict(d.pop("price"))

        plan = check_financial_price_contract_plan(d.pop("plan"))

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        delivery_mode = check_financial_price_contract_delivery_mode(d.pop("delivery_mode"))

        financial_price_contract = cls(
            price=price,
            plan=plan,
            effective_from=effective_from,
            delivery_mode=delivery_mode,
        )

        financial_price_contract.additional_properties = d
        return financial_price_contract

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
