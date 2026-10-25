from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.billing_run_item_response import BillingRunItemResponse


T = TypeVar("T", bound="BillingRunItemListResponse")


@_attrs_define
class BillingRunItemListResponse:
    """One page of a billing run's customers, ordered by kind and ID."""

    items: list[BillingRunItemResponse]
    next_cursor: str | Unset = UNSET
    """Pass as cursor to read the next page; absent on the last page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.billing_run_item_response import BillingRunItemResponse

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = BillingRunItemResponse.from_dict(items_item_data)

            items.append(items_item)

        next_cursor = d.pop("next_cursor", UNSET)

        billing_run_item_list_response = cls(
            items=items,
            next_cursor=next_cursor,
        )

        billing_run_item_list_response.additional_properties = d
        return billing_run_item_list_response

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
