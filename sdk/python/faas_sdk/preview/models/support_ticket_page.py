from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.support_ticket import SupportTicket


T = TypeVar("T", bound="SupportTicketPage")


@_attrs_define
class SupportTicketPage:
    """Creation-ordered page of at most fifty cases; pass next_cursor as cursor."""

    items: list[SupportTicket]
    next_cursor: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_cursor: str | Unset = UNSET
        if not isinstance(self.next_cursor, Unset):
            next_cursor = str(self.next_cursor)

        field_dict: dict[str, Any] = {}

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
        from ..models.support_ticket import SupportTicket

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = SupportTicket.from_dict(items_item_data)

            items.append(items_item)

        _next_cursor = d.pop("next_cursor", UNSET)
        next_cursor: UUID | Unset
        if isinstance(_next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = UUID(_next_cursor)

        support_ticket_page = cls(
            items=items,
            next_cursor=next_cursor,
        )

        return support_ticket_page
