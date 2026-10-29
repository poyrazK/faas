from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_replay_backfill_item_response import EventReplayBackfillItemResponse


T = TypeVar("T", bound="EventReplayBackfillItemsResponse")


@_attrs_define
class EventReplayBackfillItemsResponse:
    """Stable acceptance-ordered page of per-envelope outcomes for one backfill job."""

    job_id: UUID
    items: list[EventReplayBackfillItemResponse]
    next_after: str | Unset = UNSET
    """Continue with the same job and state filter; absent when the page is final."""

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
        from ..models.event_replay_backfill_item_response import EventReplayBackfillItemResponse

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = EventReplayBackfillItemResponse.from_dict(items_item_data)

            items.append(items_item)

        next_after = d.pop("next_after", UNSET)

        event_replay_backfill_items_response = cls(
            job_id=job_id,
            items=items,
            next_after=next_after,
        )

        return event_replay_backfill_items_response
