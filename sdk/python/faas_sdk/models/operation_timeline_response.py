from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_timeline_response_state import (
    OperationTimelineResponseState,
    check_operation_timeline_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_timeline_entry import OperationTimelineEntry


T = TypeVar("T", bound="OperationTimelineResponse")


@_attrs_define
class OperationTimelineResponse:
    """Chronological page merged from operation events, attempts and completion deliveries."""

    operation_id: UUID
    state: OperationTimelineResponseState
    generation: int
    entries: list[OperationTimelineEntry]
    events_expired: bool | Unset = UNSET
    """Event history was pruned before the operation; attempts and deliveries remain."""
    next_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        state: str = self.state

        generation = self.generation

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        events_expired = self.events_expired

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "state": state,
                "generation": generation,
                "entries": entries,
            }
        )
        if events_expired is not UNSET:
            field_dict["events_expired"] = events_expired
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_timeline_entry import OperationTimelineEntry

        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        state = check_operation_timeline_response_state(d.pop("state"))

        generation = d.pop("generation")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = OperationTimelineEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        events_expired = d.pop("events_expired", UNSET)

        next_cursor = d.pop("next_cursor", UNSET)

        operation_timeline_response = cls(
            operation_id=operation_id,
            state=state,
            generation=generation,
            entries=entries,
            events_expired=events_expired,
            next_cursor=next_cursor,
        )

        return operation_timeline_response
