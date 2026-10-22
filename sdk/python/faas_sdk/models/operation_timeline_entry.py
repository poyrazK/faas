from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_timeline_entry_kind import OperationTimelineEntryKind, check_operation_timeline_entry_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_delivery_attempt import OperationDeliveryAttempt
    from ..models.operation_event import OperationEvent
    from ..models.operation_timeline_attempt import OperationTimelineAttempt


T = TypeVar("T", bound="OperationTimelineEntry")


@_attrs_define
class OperationTimelineEntry:
    """One timeline record; kind selects which of event, attempt or delivery is present."""

    at: datetime.datetime
    kind: OperationTimelineEntryKind
    event: OperationEvent | Unset = UNSET
    """Ordered retained event; execution identity may change under the same logical operation."""
    attempt: OperationTimelineAttempt | Unset = UNSET
    """One HTTP execution or workflow step attempt as read; a running attempt is not re-emitted when it ends."""
    delivery: OperationDeliveryAttempt | Unset = UNSET
    """One retained notification attempt without receiver payload, URL or raw transport errors."""

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        kind: str = self.kind

        event: dict[str, Any] | Unset = UNSET
        if not isinstance(self.event, Unset):
            event = self.event.to_dict()

        attempt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attempt, Unset):
            attempt = self.attempt.to_dict()

        delivery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.delivery, Unset):
            delivery = self.delivery.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "at": at,
                "kind": kind,
            }
        )
        if event is not UNSET:
            field_dict["event"] = event
        if attempt is not UNSET:
            field_dict["attempt"] = attempt
        if delivery is not UNSET:
            field_dict["delivery"] = delivery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_delivery_attempt import OperationDeliveryAttempt
        from ..models.operation_event import OperationEvent
        from ..models.operation_timeline_attempt import OperationTimelineAttempt

        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = check_operation_timeline_entry_kind(d.pop("kind"))

        _event = d.pop("event", UNSET)
        event: OperationEvent | Unset
        if isinstance(_event, Unset):
            event = UNSET
        else:
            event = OperationEvent.from_dict(_event)

        _attempt = d.pop("attempt", UNSET)
        attempt: OperationTimelineAttempt | Unset
        if isinstance(_attempt, Unset):
            attempt = UNSET
        else:
            attempt = OperationTimelineAttempt.from_dict(_attempt)

        _delivery = d.pop("delivery", UNSET)
        delivery: OperationDeliveryAttempt | Unset
        if isinstance(_delivery, Unset):
            delivery = UNSET
        else:
            delivery = OperationDeliveryAttempt.from_dict(_delivery)

        operation_timeline_entry = cls(
            at=at,
            kind=kind,
            event=event,
            attempt=attempt,
            delivery=delivery,
        )

        return operation_timeline_entry
