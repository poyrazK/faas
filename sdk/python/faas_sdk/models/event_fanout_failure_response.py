from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_fanout_failure_response_state import (
    EventFanoutFailureResponseState,
    check_event_fanout_failure_response_state,
)

T = TypeVar("T", bound="EventFanoutFailureResponse")


@_attrs_define
class EventFanoutFailureResponse:
    """A terminal subscription routing failure before an invocation was created."""

    event_id: str
    event_source: str
    event_type: str
    subscription_id: UUID
    state: EventFanoutFailureResponseState
    attempts: int
    last_error: str
    created_at: datetime.datetime
    """When the event was accepted."""
    failed_at: datetime.datetime
    """When recipient routing became terminal."""

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        event_type = self.event_type

        subscription_id = str(self.subscription_id)

        state: str = self.state

        attempts = self.attempts

        last_error = self.last_error

        created_at = self.created_at.isoformat()

        failed_at = self.failed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "event_type": event_type,
                "subscription_id": subscription_id,
                "state": state,
                "attempts": attempts,
                "last_error": last_error,
                "created_at": created_at,
                "failed_at": failed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        event_type = d.pop("event_type")

        subscription_id = UUID(d.pop("subscription_id"))

        state = check_event_fanout_failure_response_state(d.pop("state"))

        attempts = d.pop("attempts")

        last_error = d.pop("last_error")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        failed_at = datetime.datetime.fromisoformat(d.pop("failed_at"))

        event_fanout_failure_response = cls(
            event_id=event_id,
            event_source=event_source,
            event_type=event_type,
            subscription_id=subscription_id,
            state=state,
            attempts=attempts,
            last_error=last_error,
            created_at=created_at,
            failed_at=failed_at,
        )

        return event_fanout_failure_response
