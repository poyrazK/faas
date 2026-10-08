from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_item import EventRecoveryItem


T = TypeVar("T", bound="EventRecoveryItems")


@_attrs_define
class EventRecoveryItems:
    job_id: UUID
    items: list[EventRecoveryItem]
    next_after: int | Unset = UNSET
    """Last item position for the next page; absent on the final page."""

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "job_id": job_id,
                "items": items,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_item import EventRecoveryItem

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = EventRecoveryItem.from_dict(items_item_data)

            items.append(items_item)

        next_after = d.pop("next_after", UNSET)

        event_recovery_items = cls(
            job_id=job_id,
            items=items,
            next_after=next_after,
        )

        return event_recovery_items
