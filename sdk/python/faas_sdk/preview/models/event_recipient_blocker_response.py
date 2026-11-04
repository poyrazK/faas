from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recipient_blocker_response_reason import (
    EventRecipientBlockerResponseReason,
    check_event_recipient_blocker_response_reason,
)
from ..models.event_recipient_blocker_response_status import (
    EventRecipientBlockerResponseStatus,
    check_event_recipient_blocker_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_backlog_recipient import EventBacklogRecipient
    from ..models.event_recipient_blocker_query import EventRecipientBlockerQuery


T = TypeVar("T", bound="EventRecipientBlockerResponse")


@_attrs_define
class EventRecipientBlockerResponse:
    """Current retained routing evidence for one acceptance-guarded consumer query."""

    query: EventRecipientBlockerQuery
    """Event and consumer identity with the original acceptance instant for a read-only lookup."""
    observed_at: datetime.datetime
    status: EventRecipientBlockerResponseStatus
    reason: EventRecipientBlockerResponseReason | Unset = UNSET
    routing_state: str | Unset = UNSET
    """Retained routing state; never an invocation state."""
    recipient: EventBacklogRecipient | Unset = UNSET
    """One application or workflow event recipient awaiting routing, without envelope data."""

    def to_dict(self) -> dict[str, Any]:
        query = self.query.to_dict()

        observed_at = self.observed_at.isoformat()

        status: str = self.status

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        routing_state = self.routing_state

        recipient: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recipient, Unset):
            recipient = self.recipient.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "query": query,
                "observed_at": observed_at,
                "status": status,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if routing_state is not UNSET:
            field_dict["routing_state"] = routing_state
        if recipient is not UNSET:
            field_dict["recipient"] = recipient

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_backlog_recipient import EventBacklogRecipient
        from ..models.event_recipient_blocker_query import EventRecipientBlockerQuery

        d = dict(src_dict)
        query = EventRecipientBlockerQuery.from_dict(d.pop("query"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        status = check_event_recipient_blocker_response_status(d.pop("status"))

        _reason = d.pop("reason", UNSET)
        reason: EventRecipientBlockerResponseReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_event_recipient_blocker_response_reason(_reason)

        routing_state = d.pop("routing_state", UNSET)

        _recipient = d.pop("recipient", UNSET)
        recipient: EventBacklogRecipient | Unset
        if isinstance(_recipient, Unset):
            recipient = UNSET
        else:
            recipient = EventBacklogRecipient.from_dict(_recipient)

        event_recipient_blocker_response = cls(
            query=query,
            observed_at=observed_at,
            status=status,
            reason=reason,
            routing_state=routing_state,
            recipient=recipient,
        )

        return event_recipient_blocker_response
