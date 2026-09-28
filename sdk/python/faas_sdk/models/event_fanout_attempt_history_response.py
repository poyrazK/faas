from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_fanout_attempt_response import EventFanoutAttemptResponse


T = TypeVar("T", bound="EventFanoutAttemptHistoryResponse")


@_attrs_define
class EventFanoutAttemptHistoryResponse:
    """Bounded immutable routing history for one app-scoped event identity."""

    app_slug: str
    event_source: str
    event_id: str
    history: list[EventFanoutAttemptResponse]
    subscription_id: UUID | Unset = UNSET
    next_before: str | Unset = UNSET
    """Opaque cursor bound to the app"""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        event_source = self.event_source

        event_id = self.event_id

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        subscription_id: str | Unset = UNSET
        if not isinstance(self.subscription_id, Unset):
            subscription_id = str(self.subscription_id)

        next_before = self.next_before

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "event_source": event_source,
                "event_id": event_id,
                "history": history,
            }
        )
        if subscription_id is not UNSET:
            field_dict["subscription_id"] = subscription_id
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_fanout_attempt_response import EventFanoutAttemptResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = EventFanoutAttemptResponse.from_dict(history_item_data)

            history.append(history_item)

        _subscription_id = d.pop("subscription_id", UNSET)
        subscription_id: UUID | Unset
        if isinstance(_subscription_id, Unset):
            subscription_id = UNSET
        else:
            subscription_id = UUID(_subscription_id)

        next_before = d.pop("next_before", UNSET)

        event_fanout_attempt_history_response = cls(
            app_slug=app_slug,
            event_source=event_source,
            event_id=event_id,
            history=history,
            subscription_id=subscription_id,
            next_before=next_before,
        )

        return event_fanout_attempt_history_response
