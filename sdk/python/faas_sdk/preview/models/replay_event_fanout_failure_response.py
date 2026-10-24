from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.replay_event_fanout_failure_response_state import (
    ReplayEventFanoutFailureResponseState,
    check_replay_event_fanout_failure_response_state,
)

T = TypeVar("T", bound="ReplayEventFanoutFailureResponse")


@_attrs_define
class ReplayEventFanoutFailureResponse:
    """One recipient requeued from its acceptance-time event snapshot."""

    event_id: str
    event_source: str
    subscription_id: UUID
    state: ReplayEventFanoutFailureResponseState

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        subscription_id = str(self.subscription_id)

        state: str = self.state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "subscription_id": subscription_id,
                "state": state,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        subscription_id = UUID(d.pop("subscription_id"))

        state = check_replay_event_fanout_failure_response_state(d.pop("state"))

        replay_event_fanout_failure_response = cls(
            event_id=event_id,
            event_source=event_source,
            subscription_id=subscription_id,
            state=state,
        )

        return replay_event_fanout_failure_response
