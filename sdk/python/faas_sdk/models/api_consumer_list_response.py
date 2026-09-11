from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_consumer_response import APIConsumerResponse


T = TypeVar("T", bound="APIConsumerListResponse")


@_attrs_define
class APIConsumerListResponse:
    """Stable API consumer identities for an app."""

    consumers: list[APIConsumerResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumers = []
        for consumers_item_data in self.consumers:
            consumers_item = consumers_item_data.to_dict()
            consumers.append(consumers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumers": consumers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_response import APIConsumerResponse

        d = dict(src_dict)
        consumers = []
        _consumers = d.pop("consumers")
        for consumers_item_data in _consumers:
            consumers_item = APIConsumerResponse.from_dict(consumers_item_data)

            consumers.append(consumers_item)

        api_consumer_list_response = cls(
            consumers=consumers,
        )

        api_consumer_list_response.additional_properties = d
        return api_consumer_list_response

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
