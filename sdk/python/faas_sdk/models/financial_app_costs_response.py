from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_app_costs_response_currency import (
    FinancialAppCostsResponseCurrency,
    check_financial_app_costs_response_currency,
)

if TYPE_CHECKING:
    from ..models.financial_app_meter_costs import FinancialAppMeterCosts


T = TypeVar("T", bound="FinancialAppCostsResponse")


@_attrs_define
class FinancialAppCostsResponse:
    """Retained usage costs directly attributed to one application; invoice and forecast amounts remain account scoped."""

    app_id: str
    app_slug: str
    currency: FinancialAppCostsResponseCurrency
    period_start: datetime.datetime
    period_end: datetime.datetime
    as_of: datetime.datetime
    known_usage_millicents: int
    meters: list[FinancialAppMeterCosts]
    scope: str
    scope_description: str
    bill_estimate_available: bool
    missing_bill_components: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        app_slug = self.app_slug

        currency: str = self.currency

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        as_of = self.as_of.isoformat()

        known_usage_millicents = self.known_usage_millicents

        meters = []
        for meters_item_data in self.meters:
            meters_item = meters_item_data.to_dict()
            meters.append(meters_item)

        scope = self.scope

        scope_description = self.scope_description

        bill_estimate_available = self.bill_estimate_available

        missing_bill_components = self.missing_bill_components

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "app_slug": app_slug,
                "currency": currency,
                "period_start": period_start,
                "period_end": period_end,
                "as_of": as_of,
                "known_usage_millicents": known_usage_millicents,
                "meters": meters,
                "scope": scope,
                "scope_description": scope_description,
                "bill_estimate_available": bill_estimate_available,
                "missing_bill_components": missing_bill_components,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_app_meter_costs import FinancialAppMeterCosts

        d = dict(src_dict)
        app_id = d.pop("app_id")

        app_slug = d.pop("app_slug")

        currency = check_financial_app_costs_response_currency(d.pop("currency"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        known_usage_millicents = d.pop("known_usage_millicents")

        meters = []
        _meters = d.pop("meters")
        for meters_item_data in _meters:
            meters_item = FinancialAppMeterCosts.from_dict(meters_item_data)

            meters.append(meters_item)

        scope = d.pop("scope")

        scope_description = d.pop("scope_description")

        bill_estimate_available = d.pop("bill_estimate_available")

        missing_bill_components = cast(list[str], d.pop("missing_bill_components"))

        financial_app_costs_response = cls(
            app_id=app_id,
            app_slug=app_slug,
            currency=currency,
            period_start=period_start,
            period_end=period_end,
            as_of=as_of,
            known_usage_millicents=known_usage_millicents,
            meters=meters,
            scope=scope,
            scope_description=scope_description,
            bill_estimate_available=bill_estimate_available,
            missing_bill_components=missing_bill_components,
        )

        financial_app_costs_response.additional_properties = d
        return financial_app_costs_response

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
