from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_forecast_currency import FinancialForecastCurrency, check_financial_forecast_currency
from ..types import UNSET, Unset

T = TypeVar("T", bound="FinancialForecast")


@_attrs_define
class FinancialForecast:
    """Quantity run-rate projection; absent amounts mean unavailable, never zero."""

    method: str
    available: bool
    account_id: str
    period_start: datetime.datetime
    period_end: datetime.datetime
    complete_through: datetime.datetime
    price_version: str
    meter: str
    currency: FinancialForecastCurrency
    reason: str | Unset = UNSET
    projected_quantity: int | Unset = UNSET
    projected_net_millicents: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        available = self.available

        account_id = self.account_id

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        complete_through = self.complete_through.isoformat()

        price_version = self.price_version

        meter = self.meter

        currency: str = self.currency

        reason = self.reason

        projected_quantity = self.projected_quantity

        projected_net_millicents = self.projected_net_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "available": available,
                "account_id": account_id,
                "period_start": period_start,
                "period_end": period_end,
                "complete_through": complete_through,
                "price_version": price_version,
                "meter": meter,
                "currency": currency,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if projected_quantity is not UNSET:
            field_dict["projected_quantity"] = projected_quantity
        if projected_net_millicents is not UNSET:
            field_dict["projected_net_millicents"] = projected_net_millicents

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = d.pop("method")

        available = d.pop("available")

        account_id = d.pop("account_id")

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        complete_through = datetime.datetime.fromisoformat(d.pop("complete_through"))

        price_version = d.pop("price_version")

        meter = d.pop("meter")

        currency = check_financial_forecast_currency(d.pop("currency"))

        reason = d.pop("reason", UNSET)

        projected_quantity = d.pop("projected_quantity", UNSET)

        projected_net_millicents = d.pop("projected_net_millicents", UNSET)

        financial_forecast = cls(
            method=method,
            available=available,
            account_id=account_id,
            period_start=period_start,
            period_end=period_end,
            complete_through=complete_through,
            price_version=price_version,
            meter=meter,
            currency=currency,
            reason=reason,
            projected_quantity=projected_quantity,
            projected_net_millicents=projected_net_millicents,
        )

        financial_forecast.additional_properties = d
        return financial_forecast

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
