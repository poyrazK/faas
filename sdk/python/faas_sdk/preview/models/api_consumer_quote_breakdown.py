from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerQuoteBreakdown")


@_attrs_define
class APIConsumerQuoteBreakdown:
    """Units and charge allocated to a route or meter at one rate."""

    unit: str
    price_millicents_per_unit: int
    billable_units: int
    included_units: int
    """Units covered by the flat card's monthly allowance."""
    charged_units: int
    amount_millicents: int
    route: str | Unset = UNSET
    """Method and route template for attributed request units."""
    model: str | Unset = UNSET
    rate_card_id: UUID | Unset = UNSET
    waived_units: int | Unset = UNSET
    """Reported model units waived in this breakdown."""
    trial: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        unit = self.unit

        price_millicents_per_unit = self.price_millicents_per_unit

        billable_units = self.billable_units

        included_units = self.included_units

        charged_units = self.charged_units

        amount_millicents = self.amount_millicents

        route = self.route

        model = self.model

        rate_card_id: str | Unset = UNSET
        if not isinstance(self.rate_card_id, Unset):
            rate_card_id = str(self.rate_card_id)

        waived_units = self.waived_units

        trial = self.trial

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "unit": unit,
                "price_millicents_per_unit": price_millicents_per_unit,
                "billable_units": billable_units,
                "included_units": included_units,
                "charged_units": charged_units,
                "amount_millicents": amount_millicents,
            }
        )
        if route is not UNSET:
            field_dict["route"] = route
        if model is not UNSET:
            field_dict["model"] = model
        if rate_card_id is not UNSET:
            field_dict["rate_card_id"] = rate_card_id
        if waived_units is not UNSET:
            field_dict["waived_units"] = waived_units
        if trial is not UNSET:
            field_dict["trial"] = trial

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        unit = d.pop("unit")

        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        billable_units = d.pop("billable_units")

        included_units = d.pop("included_units")

        charged_units = d.pop("charged_units")

        amount_millicents = d.pop("amount_millicents")

        route = d.pop("route", UNSET)

        model = d.pop("model", UNSET)

        _rate_card_id = d.pop("rate_card_id", UNSET)
        rate_card_id: UUID | Unset
        if isinstance(_rate_card_id, Unset):
            rate_card_id = UNSET
        else:
            rate_card_id = UUID(_rate_card_id)

        waived_units = d.pop("waived_units", UNSET)

        trial = d.pop("trial", UNSET)

        api_consumer_quote_breakdown = cls(
            unit=unit,
            price_millicents_per_unit=price_millicents_per_unit,
            billable_units=billable_units,
            included_units=included_units,
            charged_units=charged_units,
            amount_millicents=amount_millicents,
            route=route,
            model=model,
            rate_card_id=rate_card_id,
            waived_units=waived_units,
            trial=trial,
        )

        api_consumer_quote_breakdown.additional_properties = d
        return api_consumer_quote_breakdown

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
