from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_notification_acknowledgement_status import (
    EventRecoveryNotificationAcknowledgementStatus,
    check_event_recovery_notification_acknowledgement_status,
)
from ..models.event_recovery_notification_capture_status import (
    EventRecoveryNotificationCaptureStatus,
    check_event_recovery_notification_capture_status,
)
from ..models.event_recovery_notification_event import (
    EventRecoveryNotificationEvent,
    check_event_recovery_notification_event,
)
from ..models.event_recovery_notification_evidence_source import (
    EventRecoveryNotificationEvidenceSource,
    check_event_recovery_notification_evidence_source,
)
from ..models.event_recovery_notification_kind import (
    EventRecoveryNotificationKind,
    check_event_recovery_notification_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_notification_receiver import EventRecoveryNotificationReceiver


T = TypeVar("T", bound="EventRecoveryNotification")


@_attrs_define
class EventRecoveryNotification:
    """Receiver selection is frozen at capture, including an empty selection. Retained deliveries alone cannot prove the
    complete selection. Counts describe observed receivers and may be lower bounds. Unknown receivers prevent
    acknowledged status; no_receivers is distinct from acknowledgement. Pending captures have no receiver selection yet.

    """

    kind: EventRecoveryNotificationKind
    capture_status: EventRecoveryNotificationCaptureStatus
    evidence_source: EventRecoveryNotificationEvidenceSource
    recipients_known: bool
    counts_complete: bool
    acknowledgement_status: EventRecoveryNotificationAcknowledgementStatus
    pending_count: int
    in_flight_count: int
    succeeded_count: int
    failed_count: int
    dead_count: int
    awaiting_relay_count: int
    unknown_count: int
    receivers: list[EventRecoveryNotificationReceiver]
    event: EventRecoveryNotificationEvent | Unset = UNSET
    event_id: UUID | Unset = UNSET
    captured_at: datetime.datetime | Unset = UNSET
    selected_recipient_count: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        capture_status: str = self.capture_status

        evidence_source: str = self.evidence_source

        recipients_known = self.recipients_known

        counts_complete = self.counts_complete

        acknowledgement_status: str = self.acknowledgement_status

        pending_count = self.pending_count

        in_flight_count = self.in_flight_count

        succeeded_count = self.succeeded_count

        failed_count = self.failed_count

        dead_count = self.dead_count

        awaiting_relay_count = self.awaiting_relay_count

        unknown_count = self.unknown_count

        receivers = []
        for receivers_item_data in self.receivers:
            receivers_item = receivers_item_data.to_dict()
            receivers.append(receivers_item)

        event: str | Unset = UNSET
        if not isinstance(self.event, Unset):
            event = self.event

        event_id: str | Unset = UNSET
        if not isinstance(self.event_id, Unset):
            event_id = str(self.event_id)

        captured_at: str | Unset = UNSET
        if not isinstance(self.captured_at, Unset):
            captured_at = self.captured_at.isoformat()

        selected_recipient_count = self.selected_recipient_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "capture_status": capture_status,
                "evidence_source": evidence_source,
                "recipients_known": recipients_known,
                "counts_complete": counts_complete,
                "acknowledgement_status": acknowledgement_status,
                "pending_count": pending_count,
                "in_flight_count": in_flight_count,
                "succeeded_count": succeeded_count,
                "failed_count": failed_count,
                "dead_count": dead_count,
                "awaiting_relay_count": awaiting_relay_count,
                "unknown_count": unknown_count,
                "receivers": receivers,
            }
        )
        if event is not UNSET:
            field_dict["event"] = event
        if event_id is not UNSET:
            field_dict["event_id"] = event_id
        if captured_at is not UNSET:
            field_dict["captured_at"] = captured_at
        if selected_recipient_count is not UNSET:
            field_dict["selected_recipient_count"] = selected_recipient_count

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_receiver import EventRecoveryNotificationReceiver

        d = dict(src_dict)
        kind = check_event_recovery_notification_kind(d.pop("kind"))

        capture_status = check_event_recovery_notification_capture_status(d.pop("capture_status"))

        evidence_source = check_event_recovery_notification_evidence_source(d.pop("evidence_source"))

        recipients_known = d.pop("recipients_known")

        counts_complete = d.pop("counts_complete")

        acknowledgement_status = check_event_recovery_notification_acknowledgement_status(
            d.pop("acknowledgement_status")
        )

        pending_count = d.pop("pending_count")

        in_flight_count = d.pop("in_flight_count")

        succeeded_count = d.pop("succeeded_count")

        failed_count = d.pop("failed_count")

        dead_count = d.pop("dead_count")

        awaiting_relay_count = d.pop("awaiting_relay_count")

        unknown_count = d.pop("unknown_count")

        receivers = []
        _receivers = d.pop("receivers")
        for receivers_item_data in _receivers:
            receivers_item = EventRecoveryNotificationReceiver.from_dict(receivers_item_data)

            receivers.append(receivers_item)

        _event = d.pop("event", UNSET)
        event: EventRecoveryNotificationEvent | Unset
        if isinstance(_event, Unset):
            event = UNSET
        else:
            event = check_event_recovery_notification_event(_event)

        _event_id = d.pop("event_id", UNSET)
        event_id: UUID | Unset
        if isinstance(_event_id, Unset):
            event_id = UNSET
        else:
            event_id = UUID(_event_id)

        _captured_at = d.pop("captured_at", UNSET)
        captured_at: datetime.datetime | Unset
        if isinstance(_captured_at, Unset):
            captured_at = UNSET
        else:
            captured_at = datetime.datetime.fromisoformat(_captured_at)

        selected_recipient_count = d.pop("selected_recipient_count", UNSET)

        event_recovery_notification = cls(
            kind=kind,
            capture_status=capture_status,
            evidence_source=evidence_source,
            recipients_known=recipients_known,
            counts_complete=counts_complete,
            acknowledgement_status=acknowledgement_status,
            pending_count=pending_count,
            in_flight_count=in_flight_count,
            succeeded_count=succeeded_count,
            failed_count=failed_count,
            dead_count=dead_count,
            awaiting_relay_count=awaiting_relay_count,
            unknown_count=unknown_count,
            receivers=receivers,
            event=event,
            event_id=event_id,
            captured_at=captured_at,
            selected_recipient_count=selected_recipient_count,
        )

        return event_recovery_notification
