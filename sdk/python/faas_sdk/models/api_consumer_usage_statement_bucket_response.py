from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerUsageStatementBucketResponse")


@_attrs_define
class APIConsumerUsageStatementBucketResponse:
    """One UTC minute captured in a durable usage statement."""

    window_start: datetime.datetime
    billable_units: int
    charged_units: int
    """Units this revision bills at the price; the rest are covered by the monthly allowance. An adjustment may
    charge units it does not add when late usage exhausted the allowance sooner."""
    amount_millicents: int
    """Exact charge for this minute. Negative only on a tiered adjustment line that re-rates billed units into a
    cheaper step; a revision's total is never negative."""
    rate_card_id: UUID | Unset = UNSET
    currency: str | Unset = UNSET
    price_millicents_per_unit: int | Unset = UNSET
    tier_units: list[int] | Unset = UNSET
    """Units per step of a tiered rate card's ladder. In an adjustment revision, entries are differences and can be
    negative when late usage moved billed units into a cheaper step."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window_start = self.window_start.isoformat()

        billable_units = self.billable_units

        charged_units = self.charged_units

        amount_millicents = self.amount_millicents

        rate_card_id: str | Unset = UNSET
        if not isinstance(self.rate_card_id, Unset):
            rate_card_id = str(self.rate_card_id)

        currency = self.currency

        price_millicents_per_unit = self.price_millicents_per_unit

        tier_units: list[int] | Unset = UNSET
        if not isinstance(self.tier_units, Unset):
            tier_units = self.tier_units

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window_start": window_start,
                "billable_units": billable_units,
                "charged_units": charged_units,
                "amount_millicents": amount_millicents,
            }
        )
        if rate_card_id is not UNSET:
            field_dict["rate_card_id"] = rate_card_id
        if currency is not UNSET:
            field_dict["currency"] = currency
        if price_millicents_per_unit is not UNSET:
            field_dict["price_millicents_per_unit"] = price_millicents_per_unit
        if tier_units is not UNSET:
            field_dict["tier_units"] = tier_units

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        billable_units = d.pop("billable_units")

        charged_units = d.pop("charged_units")

        amount_millicents = d.pop("amount_millicents")

        _rate_card_id = d.pop("rate_card_id", UNSET)
        rate_card_id: UUID | Unset
        if isinstance(_rate_card_id, Unset):
            rate_card_id = UNSET
        else:
            rate_card_id = UUID(_rate_card_id)

        currency = d.pop("currency", UNSET)

        price_millicents_per_unit = d.pop("price_millicents_per_unit", UNSET)

        tier_units = cast(list[int], d.pop("tier_units", UNSET))

        api_consumer_usage_statement_bucket_response = cls(
            window_start=window_start,
            billable_units=billable_units,
            charged_units=charged_units,
            amount_millicents=amount_millicents,
            rate_card_id=rate_card_id,
            currency=currency,
            price_millicents_per_unit=price_millicents_per_unit,
            tier_units=tier_units,
        )

        api_consumer_usage_statement_bucket_response.additional_properties = d
        return api_consumer_usage_statement_bucket_response

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
