from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_receipt_execution_response import EventReceiptExecutionResponse


T = TypeVar("T", bound="EventReceiptReplayHistoryResponse")


@_attrs_define
class EventReceiptReplayHistoryResponse:
    """A page of retained generic replay executions for one captured event consumer."""

    event_source: str
    event_id: str
    subscription_id: str
    original_invocation_id: UUID
    """Deterministic root delivery ID; its execution record may have expired."""
    replays: list[EventReceiptExecutionResponse]
    next_after: str | Unset = UNSET
    """Opaque cursor for the next older page; omit after when starting a fresh history read."""

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        subscription_id = self.subscription_id

        original_invocation_id = str(self.original_invocation_id)

        replays = []
        for replays_item_data in self.replays:
            replays_item = replays_item_data.to_dict()
            replays.append(replays_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "subscription_id": subscription_id,
                "original_invocation_id": original_invocation_id,
                "replays": replays,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_receipt_execution_response import EventReceiptExecutionResponse

        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        subscription_id = d.pop("subscription_id")

        original_invocation_id = UUID(d.pop("original_invocation_id"))

        replays = []
        _replays = d.pop("replays")
        for replays_item_data in _replays:
            replays_item = EventReceiptExecutionResponse.from_dict(replays_item_data)

            replays.append(replays_item)

        next_after = d.pop("next_after", UNSET)

        event_receipt_replay_history_response = cls(
            event_source=event_source,
            event_id=event_id,
            subscription_id=subscription_id,
            original_invocation_id=original_invocation_id,
            replays=replays,
            next_after=next_after,
        )

        return event_receipt_replay_history_response
