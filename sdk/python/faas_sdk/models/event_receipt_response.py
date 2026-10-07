from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_receipt_response_routing_mode import (
    EventReceiptResponseRoutingMode,
    check_event_receipt_response_routing_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_receipt_recipient_response import EventReceiptRecipientResponse
    from ..models.event_receipt_response_routing_summary import EventReceiptResponseRoutingSummary


T = TypeVar("T", bound="EventReceiptResponse")


@_attrs_define
class EventReceiptResponse:
    """Acceptance and routing evidence with a bounded recipient page; execution is a separate lifecycle."""

    event_id: str
    event_source: str
    event_type: str
    accepted_at: datetime.datetime
    """First durable acceptance time, preserved on identical retries."""
    snapshot_captured: bool
    """False means legacy recipient membership is unknown, rather than an empty matching set."""
    routing_mode: EventReceiptResponseRoutingMode
    """Whole-event legacy routing or independent recipient leases."""
    recipient_count: int
    """Total captured candidates across all pages; unknown when snapshot_captured=false."""
    routing_summary: EventReceiptResponseRoutingSummary
    """Counts across all captured recipients by current routing checkpoint state."""
    recipients: list[EventReceiptRecipientResponse]
    client_event_id: str | Unset = UNSET
    """Original caller-chosen identifier for tenant-scoped events."""
    schema_version: str | Unset = UNSET
    routing_settled_at: datetime.datetime | Unset = UNSET
    """All routing candidates settled, including filtered and failed outcomes; absent while routing is active."""
    retain_until: datetime.datetime | Unset = UNSET
    """Settled receipt retention boundary, 30 days after routing settled. Absent for active routing."""
    next_after: str | Unset = UNSET
    """Opaque cursor for the next acceptance-ordered recipient page."""

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        event_type = self.event_type

        accepted_at = self.accepted_at.isoformat()

        snapshot_captured = self.snapshot_captured

        routing_mode: str = self.routing_mode

        recipient_count = self.recipient_count

        routing_summary = self.routing_summary.to_dict()

        recipients = []
        for recipients_item_data in self.recipients:
            recipients_item = recipients_item_data.to_dict()
            recipients.append(recipients_item)

        client_event_id = self.client_event_id

        schema_version = self.schema_version

        routing_settled_at: str | Unset = UNSET
        if not isinstance(self.routing_settled_at, Unset):
            routing_settled_at = self.routing_settled_at.isoformat()

        retain_until: str | Unset = UNSET
        if not isinstance(self.retain_until, Unset):
            retain_until = self.retain_until.isoformat()

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "event_type": event_type,
                "accepted_at": accepted_at,
                "snapshot_captured": snapshot_captured,
                "routing_mode": routing_mode,
                "recipient_count": recipient_count,
                "routing_summary": routing_summary,
                "recipients": recipients,
            }
        )
        if client_event_id is not UNSET:
            field_dict["client_event_id"] = client_event_id
        if schema_version is not UNSET:
            field_dict["schema_version"] = schema_version
        if routing_settled_at is not UNSET:
            field_dict["routing_settled_at"] = routing_settled_at
        if retain_until is not UNSET:
            field_dict["retain_until"] = retain_until
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_receipt_recipient_response import EventReceiptRecipientResponse
        from ..models.event_receipt_response_routing_summary import EventReceiptResponseRoutingSummary

        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        event_type = d.pop("event_type")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        snapshot_captured = d.pop("snapshot_captured")

        routing_mode = check_event_receipt_response_routing_mode(d.pop("routing_mode"))

        recipient_count = d.pop("recipient_count")

        routing_summary = EventReceiptResponseRoutingSummary.from_dict(d.pop("routing_summary"))

        recipients = []
        _recipients = d.pop("recipients")
        for recipients_item_data in _recipients:
            recipients_item = EventReceiptRecipientResponse.from_dict(recipients_item_data)

            recipients.append(recipients_item)

        client_event_id = d.pop("client_event_id", UNSET)

        schema_version = d.pop("schema_version", UNSET)

        _routing_settled_at = d.pop("routing_settled_at", UNSET)
        routing_settled_at: datetime.datetime | Unset
        if isinstance(_routing_settled_at, Unset):
            routing_settled_at = UNSET
        else:
            routing_settled_at = datetime.datetime.fromisoformat(_routing_settled_at)

        _retain_until = d.pop("retain_until", UNSET)
        retain_until: datetime.datetime | Unset
        if isinstance(_retain_until, Unset):
            retain_until = UNSET
        else:
            retain_until = datetime.datetime.fromisoformat(_retain_until)

        next_after = d.pop("next_after", UNSET)

        event_receipt_response = cls(
            event_id=event_id,
            event_source=event_source,
            event_type=event_type,
            accepted_at=accepted_at,
            snapshot_captured=snapshot_captured,
            routing_mode=routing_mode,
            recipient_count=recipient_count,
            routing_summary=routing_summary,
            recipients=recipients,
            client_event_id=client_event_id,
            schema_version=schema_version,
            routing_settled_at=routing_settled_at,
            retain_until=retain_until,
            next_after=next_after,
        )

        return event_receipt_response
