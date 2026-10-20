from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_schedule_history_event_event import (
    ManagedRealtimeScheduleHistoryEventEvent,
    check_managed_realtime_schedule_history_event_event,
)
from ..models.managed_realtime_schedule_history_event_status import (
    ManagedRealtimeScheduleHistoryEventStatus,
    check_managed_realtime_schedule_history_event_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeScheduleHistoryEvent")


@_attrs_define
class ManagedRealtimeScheduleHistoryEvent:
    """Recorded execution or lifecycle event for a channel publish schedule."""

    skipped_occurrences: int
    occurrence: int
    completed_occurrences: int
    version: int
    event: ManagedRealtimeScheduleHistoryEventEvent
    status: ManagedRealtimeScheduleHistoryEventStatus
    attempts: int
    cycle_attempts: int
    deliver_at: datetime.datetime
    occurred_at: datetime.datetime
    skip_reason: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    failure_code: str | Unset = UNSET
    sequence: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        skipped_occurrences = self.skipped_occurrences

        occurrence = self.occurrence

        completed_occurrences = self.completed_occurrences

        version = self.version

        event: str = self.event

        status: str = self.status

        attempts = self.attempts

        cycle_attempts = self.cycle_attempts

        deliver_at = self.deliver_at.isoformat()

        occurred_at = self.occurred_at.isoformat()

        skip_reason = self.skip_reason

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        failure_code = self.failure_code

        sequence = self.sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "skipped_occurrences": skipped_occurrences,
                "occurrence": occurrence,
                "completed_occurrences": completed_occurrences,
                "version": version,
                "event": event,
                "status": status,
                "attempts": attempts,
                "cycle_attempts": cycle_attempts,
                "deliver_at": deliver_at,
                "occurred_at": occurred_at,
            }
        )
        if skip_reason is not UNSET:
            field_dict["skip_reason"] = skip_reason
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if sequence is not UNSET:
            field_dict["sequence"] = sequence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        skipped_occurrences = d.pop("skipped_occurrences")

        occurrence = d.pop("occurrence")

        completed_occurrences = d.pop("completed_occurrences")

        version = d.pop("version")

        event = check_managed_realtime_schedule_history_event_event(d.pop("event"))

        status = check_managed_realtime_schedule_history_event_status(d.pop("status"))

        attempts = d.pop("attempts")

        cycle_attempts = d.pop("cycle_attempts")

        deliver_at = datetime.datetime.fromisoformat(d.pop("deliver_at"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        skip_reason = d.pop("skip_reason", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        failure_code = d.pop("failure_code", UNSET)

        sequence = d.pop("sequence", UNSET)

        managed_realtime_schedule_history_event = cls(
            skipped_occurrences=skipped_occurrences,
            occurrence=occurrence,
            completed_occurrences=completed_occurrences,
            version=version,
            event=event,
            status=status,
            attempts=attempts,
            cycle_attempts=cycle_attempts,
            deliver_at=deliver_at,
            occurred_at=occurred_at,
            skip_reason=skip_reason,
            next_attempt_at=next_attempt_at,
            failure_code=failure_code,
            sequence=sequence,
        )

        managed_realtime_schedule_history_event.additional_properties = d
        return managed_realtime_schedule_history_event

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
