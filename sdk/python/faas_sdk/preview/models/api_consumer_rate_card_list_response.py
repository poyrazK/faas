from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_consumer_rate_card_response import APIConsumerRateCardResponse


T = TypeVar("T", bound="APIConsumerRateCardListResponse")


@_attrs_define
class APIConsumerRateCardListResponse:
    """Chronological history of immutable API consumer rate cards for an app."""

    rate_cards: list[APIConsumerRateCardResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rate_cards = []
        for rate_cards_item_data in self.rate_cards:
            rate_cards_item = rate_cards_item_data.to_dict()
            rate_cards.append(rate_cards_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rate_cards": rate_cards,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_rate_card_response import APIConsumerRateCardResponse

        d = dict(src_dict)
        rate_cards = []
        _rate_cards = d.pop("rate_cards")
        for rate_cards_item_data in _rate_cards:
            rate_cards_item = APIConsumerRateCardResponse.from_dict(rate_cards_item_data)

            rate_cards.append(rate_cards_item)

        api_consumer_rate_card_list_response = cls(
            rate_cards=rate_cards,
        )

        api_consumer_rate_card_list_response.additional_properties = d
        return api_consumer_rate_card_list_response

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
