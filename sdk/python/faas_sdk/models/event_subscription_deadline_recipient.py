from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_subscription_deadline_recipient_waiting_reason import (
    EventSubscriptionDeadlineRecipientWaitingReason,
    check_event_subscription_deadline_recipient_waiting_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventSubscriptionDeadlineRecipient")


@_attrs_define
class EventSubscriptionDeadlineRecipient:
    """Payload-free identity, effective deadline and observed waiting reason of a retained pending delivery. The expiry
    flag uses the current page observation, not the fixed status-filter window.

    """

    event_source: str
    event_id: str
    accepted_at: datetime.datetime
    deadline_at: datetime.datetime
    expired: bool
    waiting_reason: EventSubscriptionDeadlineRecipientWaitingReason
    next_attempt_at: datetime.datetime | Unset = UNSET
    lease_until: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        accepted_at = self.accepted_at.isoformat()

        deadline_at = self.deadline_at.isoformat()

        expired = self.expired

        waiting_reason: str = self.waiting_reason

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "accepted_at": accepted_at,
                "deadline_at": deadline_at,
                "expired": expired,
                "waiting_reason": waiting_reason,
            }
        )
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        expired = d.pop("expired")

        waiting_reason = check_event_subscription_deadline_recipient_waiting_reason(d.pop("waiting_reason"))

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _lease_until = d.pop("lease_until", UNSET)
        lease_until: datetime.datetime | Unset
        if isinstance(_lease_until, Unset):
            lease_until = UNSET
        else:
            lease_until = datetime.datetime.fromisoformat(_lease_until)

        event_subscription_deadline_recipient = cls(
            event_source=event_source,
            event_id=event_id,
            accepted_at=accepted_at,
            deadline_at=deadline_at,
            expired=expired,
            waiting_reason=waiting_reason,
            next_attempt_at=next_attempt_at,
            lease_until=lease_until,
        )

        event_subscription_deadline_recipient.additional_properties = d
        return event_subscription_deadline_recipient

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
