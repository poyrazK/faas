from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_consumer_overdue_hold_response import APIConsumerOverdueHoldResponse


T = TypeVar("T", bound="APIConsumerOverdueHoldListResponse")


@_attrs_define
class APIConsumerOverdueHoldListResponse:
    """An app's consumers held for overdue invoices."""

    holds: list[APIConsumerOverdueHoldResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        holds = []
        for holds_item_data in self.holds:
            holds_item = holds_item_data.to_dict()
            holds.append(holds_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "holds": holds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_overdue_hold_response import APIConsumerOverdueHoldResponse

        d = dict(src_dict)
        holds = []
        _holds = d.pop("holds")
        for holds_item_data in _holds:
            holds_item = APIConsumerOverdueHoldResponse.from_dict(holds_item_data)

            holds.append(holds_item)

        api_consumer_overdue_hold_list_response = cls(
            holds=holds,
        )

        api_consumer_overdue_hold_list_response.additional_properties = d
        return api_consumer_overdue_hold_list_response

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
