from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_history_entry import EventRecoveryHistoryEntry


T = TypeVar("T", bound="EventRecoveryHistory")


@_attrs_define
class EventRecoveryHistory:
    job_id: UUID
    entries: list[EventRecoveryHistoryEntry]
    next_after: int | Unset = UNSET
    """Exclusive last ID for the next page; omitted on the final page."""

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "job_id": job_id,
                "entries": entries,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_history_entry import EventRecoveryHistoryEntry

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = EventRecoveryHistoryEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        next_after = d.pop("next_after", UNSET)

        event_recovery_history = cls(
            job_id=job_id,
            entries=entries,
            next_after=next_after,
        )

        return event_recovery_history
