from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerRateCardTier")


@_attrs_define
class APIConsumerRateCardTier:
    """One step of a graduated per-request price ladder."""

    up_to: int | None
    """Exclusive upper bound of this step's monthly position; null for the unbounded last step."""
    price_millicents_per_unit: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        up_to: int | None
        up_to = self.up_to

        price_millicents_per_unit = self.price_millicents_per_unit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "up_to": up_to,
                "price_millicents_per_unit": price_millicents_per_unit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_up_to(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        up_to = _parse_up_to(d.pop("up_to"))

        price_millicents_per_unit = d.pop("price_millicents_per_unit")

        api_consumer_rate_card_tier = cls(
            up_to=up_to,
            price_millicents_per_unit=price_millicents_per_unit,
        )

        api_consumer_rate_card_tier.additional_properties = d
        return api_consumer_rate_card_tier

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
