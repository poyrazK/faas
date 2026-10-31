from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreateAPIConsumerRateCardRequestRouteWeights")


@_attrs_define
class CreateAPIConsumerRateCardRequestRouteWeights:
    """Counts each request on a listed "METHOD /template" route as that many units (1..1000, at most 50 routes); unlisted
    routes count 1. Weighted units feed included units, tiers, and statements. Route labels match the app's declared or
    discovered route templates.

    """

    additional_properties: dict[str, int] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        create_api_consumer_rate_card_request_route_weights = cls()

        create_api_consumer_rate_card_request_route_weights.additional_properties = d
        return create_api_consumer_rate_card_request_route_weights

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> int:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: int) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
