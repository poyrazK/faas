from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_delivery_response_state import EventDeliveryResponseState, check_event_delivery_response_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventDeliveryResponse")


@_attrs_define
class EventDeliveryResponse:
    """Metadata-only lifecycle projection for one event-triggered invocation."""

    invocation_id: UUID
    event_id: str
    event_source: str
    event_type: str
    state: EventDeliveryResponseState
    attempts: int
    created_at: datetime.datetime
    subscription_id: UUID | Unset = UNSET
    last_error: str | Unset = UNSET
    completed_at: datetime.datetime | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        invocation_id = str(self.invocation_id)

        event_id = self.event_id

        event_source = self.event_source

        event_type = self.event_type

        state: str = self.state

        attempts = self.attempts

        created_at = self.created_at.isoformat()

        subscription_id: str | Unset = UNSET
        if not isinstance(self.subscription_id, Unset):
            subscription_id = str(self.subscription_id)

        last_error = self.last_error

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "invocation_id": invocation_id,
                "event_id": event_id,
                "event_source": event_source,
                "event_type": event_type,
                "state": state,
                "attempts": attempts,
                "created_at": created_at,
            }
        )
        if subscription_id is not UNSET:
            field_dict["subscription_id"] = subscription_id
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invocation_id = UUID(d.pop("invocation_id"))

        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        event_type = d.pop("event_type")

        state = check_event_delivery_response_state(d.pop("state"))

        attempts = d.pop("attempts")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _subscription_id = d.pop("subscription_id", UNSET)
        subscription_id: UUID | Unset
        if isinstance(_subscription_id, Unset):
            subscription_id = UNSET
        else:
            subscription_id = UUID(_subscription_id)

        last_error = d.pop("last_error", UNSET)

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        event_delivery_response = cls(
            invocation_id=invocation_id,
            event_id=event_id,
            event_source=event_source,
            event_type=event_type,
            state=state,
            attempts=attempts,
            created_at=created_at,
            subscription_id=subscription_id,
            last_error=last_error,
            completed_at=completed_at,
        )

        return event_delivery_response
