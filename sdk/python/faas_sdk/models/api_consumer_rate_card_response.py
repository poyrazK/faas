from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_rate_card_response_unit import (
    APIConsumerRateCardResponseUnit,
    check_api_consumer_rate_card_response_unit,
)

T = TypeVar("T", bound="APIConsumerRateCardResponse")


@_attrs_define
class APIConsumerRateCardResponse:
    """Immutable, versioned app-level price for one API request unit."""

    id: UUID
    app_id: UUID
    currency: str
    unit: APIConsumerRateCardResponseUnit
    price_millicents_per_unit: int
    effective_from: datetime.datetime
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        currency = self.currency

        unit: str = self.unit

        price_millicents_per_unit = self.price_millicents_per_unit

        effective_from = self.effective_from.isoformat()

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "currency": currency,
                "unit": unit,
                "price_millicents_per_unit": price_millicents_per_unit,
                "effective_from": effective_from,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        currency = d.pop("currency")

        unit = check_api_consumer_rate_card_response_unit(d.pop("unit"))

        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        api_consumer_rate_card_response = cls(
            id=id,
            app_id=app_id,
            currency=currency,
            unit=unit,
            price_millicents_per_unit=price_millicents_per_unit,
            effective_from=effective_from,
            created_at=created_at,
        )

        api_consumer_rate_card_response.additional_properties = d
        return api_consumer_rate_card_response

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
