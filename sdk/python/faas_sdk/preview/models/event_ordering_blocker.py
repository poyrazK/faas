from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_ordering_blocker_state import EventOrderingBlockerState, check_event_ordering_blocker_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventOrderingBlocker")


@_attrs_define
class EventOrderingBlocker:
    """Earliest unresolved routing recipient in the captured ordered lane; excludes event data and resolved work keys."""

    event_source: str
    event_id: str
    subscription_id: str
    accepted_at: datetime.datetime
    state: EventOrderingBlockerState
    age_seconds: float
    receipt_url: str
    next_attempt_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        subscription_id = self.subscription_id

        accepted_at = self.accepted_at.isoformat()

        state: str = self.state

        age_seconds = self.age_seconds

        receipt_url = self.receipt_url

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "subscription_id": subscription_id,
                "accepted_at": accepted_at,
                "state": state,
                "age_seconds": age_seconds,
                "receipt_url": receipt_url,
            }
        )
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        subscription_id = d.pop("subscription_id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        state = check_event_ordering_blocker_state(d.pop("state"))

        age_seconds = d.pop("age_seconds")

        receipt_url = d.pop("receipt_url")

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        event_ordering_blocker = cls(
            event_source=event_source,
            event_id=event_id,
            subscription_id=subscription_id,
            accepted_at=accepted_at,
            state=state,
            age_seconds=age_seconds,
            receipt_url=receipt_url,
            next_attempt_at=next_attempt_at,
        )

        return event_ordering_blocker
