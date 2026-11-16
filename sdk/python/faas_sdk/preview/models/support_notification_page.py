from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.support_notification import SupportNotification


T = TypeVar("T", bound="SupportNotificationPage")


@_attrs_define
class SupportNotificationPage:
    """Sequence-ordered page of at most fifty updates in the authorized scope."""

    items: list[SupportNotification]
    unread_count: int
    next_cursor: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        unread_count = self.unread_count

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "unread_count": unread_count,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.support_notification import SupportNotification

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = SupportNotification.from_dict(items_item_data)

            items.append(items_item)

        unread_count = d.pop("unread_count")

        next_cursor = d.pop("next_cursor", UNSET)

        support_notification_page = cls(
            items=items,
            unread_count=unread_count,
            next_cursor=next_cursor,
        )

        return support_notification_page
