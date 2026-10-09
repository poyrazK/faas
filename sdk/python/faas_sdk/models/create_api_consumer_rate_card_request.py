from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier


T = TypeVar("T", bound="CreateAPIConsumerRateCardRequest")


@_attrs_define
class CreateAPIConsumerRateCardRequest:
    """Immutable app-level request price. Currency defaults to EUR and effective_from defaults to the next UTC minute."""

    price_millicents_per_unit: int
    currency: str | Unset = "EUR"
    included_units_per_month: int | Unset = 0
    """Free request units per consumer per UTC calendar month while this card is effective, consumed in minute order.
    Once any card includes units, effective_from cannot be in the past."""
    tiers: list[APIConsumerRateCardTier] | Unset = UNSET
    """Optional graduated ladder that replaces price_millicents_per_unit and included_units_per_month. Each consumer's
    units are counted per UTC calendar month in minute order and priced by the step their position falls in. Bounds
    increase strictly, only the last step is unbounded, and only the first step may be free. Statements of periods
    priced by a tiered card must cover exactly one UTC calendar month."""
    effective_from: datetime.datetime | None | Unset = UNSET
    """UTC minute at which this version starts; omitted means the next UTC minute."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        price_millicents_per_unit = self.price_millicents_per_unit

        currency = self.currency

        included_units_per_month = self.included_units_per_month

        tiers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.tiers, Unset):
            tiers = []
            for tiers_item_data in self.tiers:
                tiers_item = tiers_item_data.to_dict()
                tiers.append(tiers_item)

        effective_from: None | str | Unset
        if isinstance(self.effective_from, Unset):
            effective_from = UNSET
        elif isinstance(self.effective_from, datetime.datetime):
            effective_from = self.effective_from.isoformat()
        else:
            effective_from = self.effective_from

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "price_millicents_per_unit": price_millicents_per_unit,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency
        if included_units_per_month is not UNSET:
            field_dict["included_units_per_month"] = included_units_per_month
        if tiers is not UNSET:
            field_dict["tiers"] = tiers
        if effective_from is not UNSET:
            field_dict["effective_from"] = effective_from

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_tier import APIConsumerRateCardTier

        d = dict(src_dict)
        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        currency = d.pop("currency", UNSET)

        included_units_per_month = d.pop("included_units_per_month", UNSET)

        _tiers = d.pop("tiers", UNSET)
        tiers: list[APIConsumerRateCardTier] | Unset = UNSET
        if _tiers is not UNSET:
            tiers = []
            for tiers_item_data in _tiers:
                tiers_item = APIConsumerRateCardTier.from_dict(tiers_item_data)

                tiers.append(tiers_item)

        def _parse_effective_from(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                effective_from_type_0 = datetime.datetime.fromisoformat(data)

                return effective_from_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        effective_from = _parse_effective_from(d.pop("effective_from", UNSET))

        create_api_consumer_rate_card_request = cls(
            price_millicents_per_unit=price_millicents_per_unit,
            currency=currency,
            included_units_per_month=included_units_per_month,
            tiers=tiers,
            effective_from=effective_from,
        )

        create_api_consumer_rate_card_request.additional_properties = d
        return create_api_consumer_rate_card_request

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
