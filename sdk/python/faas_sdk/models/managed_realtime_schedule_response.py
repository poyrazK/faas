from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_schedule_request_on_condition_failure import (
    ManagedRealtimeScheduleRequestOnConditionFailure,
    check_managed_realtime_schedule_request_on_condition_failure,
)
from ..models.managed_realtime_schedule_response_status import (
    ManagedRealtimeScheduleResponseStatus,
    check_managed_realtime_schedule_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_schedule_request_metadata import ManagedRealtimeScheduleRequestMetadata


T = TypeVar("T", bound="ManagedRealtimeScheduleResponse")


@_attrs_define
class ManagedRealtimeScheduleResponse:
    data_base64: str
    """At most 4096 decoded bytes."""
    deliver_at: datetime.datetime
    """Future time within 30 days of creation."""
    initial_deliver_at: datetime.datetime
    skipped_occurrences: int
    occurrence: int
    completed_occurrences: int
    attempts: int
    """Lifetime recorded delivery attempts."""
    cycle_attempts: int
    schedule_id: str
    channel: str
    version: int
    status: ManagedRealtimeScheduleResponseStatus
    created_at: datetime.datetime
    updated_at: datetime.datetime
    max_attempts: int = 1
    """Total attempts per retry cycle; one disables automatic retries."""
    backoff_seconds: int = 5
    """Initial retry delay, doubled per failure up to one hour."""
    group: str | Unset = UNSET
    """Optional immutable channel-scoped group label; at most 128 UTF-8 bytes."""
    conditions: list[Any] | Unset = UNSET
    """AND predicates over reducer state in this channel; at most 4 KiB encoded. Each predicate has a key and
    exactly one operator."""
    on_condition_failure: ManagedRealtimeScheduleRequestOnConditionFailure | Unset = "retry"
    """Requires conditions; retry follows the configured attempt budget."""
    interval_seconds: int | Unset = UNSET
    """Fixed delay after each successful occurrence; omit for one-time publication."""
    max_occurrences: int | Unset = UNSET
    """Optional completed-slot limit (published or intentionally skipped); zero is unlimited."""
    end_at: datetime.datetime | Unset = UNSET
    """Last permitted planned occurrence time, within one year of initial delivery."""
    binary: bool | Unset = False
    metadata: ManagedRealtimeScheduleRequestMetadata | Unset = UNSET
    skip_reason: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    """Next pending retry time; omitted after completion or cancellation."""
    last_attempt_at: datetime.datetime | Unset = UNSET
    sequence: int | Unset = UNSET
    """Committed history sequence when published."""
    last_error: str | Unset = UNSET
    """Most recent recorded delivery failure code; retained after a successful retry."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_base64 = self.data_base64

        deliver_at = self.deliver_at.isoformat()

        max_attempts = self.max_attempts

        backoff_seconds = self.backoff_seconds

        initial_deliver_at = self.initial_deliver_at.isoformat()

        skipped_occurrences = self.skipped_occurrences

        occurrence = self.occurrence

        completed_occurrences = self.completed_occurrences

        attempts = self.attempts

        cycle_attempts = self.cycle_attempts

        schedule_id = self.schedule_id

        channel = self.channel

        version = self.version

        status: str = self.status

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        group = self.group

        conditions: list[Any] | Unset = UNSET
        if not isinstance(self.conditions, Unset):
            conditions = []
            for conditions_item_data in self.conditions:
                conditions_item: Any
                conditions_item = conditions_item_data
                conditions.append(conditions_item)

        on_condition_failure: str | Unset = UNSET
        if not isinstance(self.on_condition_failure, Unset):
            on_condition_failure = self.on_condition_failure

        interval_seconds = self.interval_seconds

        max_occurrences = self.max_occurrences

        end_at: str | Unset = UNSET
        if not isinstance(self.end_at, Unset):
            end_at = self.end_at.isoformat()

        binary = self.binary

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        skip_reason = self.skip_reason

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        last_attempt_at: str | Unset = UNSET
        if not isinstance(self.last_attempt_at, Unset):
            last_attempt_at = self.last_attempt_at.isoformat()

        sequence = self.sequence

        last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_base64": data_base64,
                "deliver_at": deliver_at,
                "max_attempts": max_attempts,
                "backoff_seconds": backoff_seconds,
                "initial_deliver_at": initial_deliver_at,
                "skipped_occurrences": skipped_occurrences,
                "occurrence": occurrence,
                "completed_occurrences": completed_occurrences,
                "attempts": attempts,
                "cycle_attempts": cycle_attempts,
                "schedule_id": schedule_id,
                "channel": channel,
                "version": version,
                "status": status,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if group is not UNSET:
            field_dict["group"] = group
        if conditions is not UNSET:
            field_dict["conditions"] = conditions
        if on_condition_failure is not UNSET:
            field_dict["on_condition_failure"] = on_condition_failure
        if interval_seconds is not UNSET:
            field_dict["interval_seconds"] = interval_seconds
        if max_occurrences is not UNSET:
            field_dict["max_occurrences"] = max_occurrences
        if end_at is not UNSET:
            field_dict["end_at"] = end_at
        if binary is not UNSET:
            field_dict["binary"] = binary
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if skip_reason is not UNSET:
            field_dict["skip_reason"] = skip_reason
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if last_attempt_at is not UNSET:
            field_dict["last_attempt_at"] = last_attempt_at
        if sequence is not UNSET:
            field_dict["sequence"] = sequence
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_schedule_request_metadata import ManagedRealtimeScheduleRequestMetadata

        d = dict(src_dict)
        data_base64 = d.pop("data_base64")

        deliver_at = datetime.datetime.fromisoformat(d.pop("deliver_at"))

        max_attempts = d.pop("max_attempts")

        backoff_seconds = d.pop("backoff_seconds")

        initial_deliver_at = datetime.datetime.fromisoformat(d.pop("initial_deliver_at"))

        skipped_occurrences = d.pop("skipped_occurrences")

        occurrence = d.pop("occurrence")

        completed_occurrences = d.pop("completed_occurrences")

        attempts = d.pop("attempts")

        cycle_attempts = d.pop("cycle_attempts")

        schedule_id = d.pop("schedule_id")

        channel = d.pop("channel")

        version = d.pop("version")

        status = check_managed_realtime_schedule_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        group = d.pop("group", UNSET)

        _conditions = d.pop("conditions", UNSET)
        conditions: list[Any] | Unset = UNSET
        if _conditions is not UNSET:
            conditions = []
            for conditions_item_data in _conditions:

                def _parse_conditions_item(data: object) -> Any:
                    return cast(Any, data)

                conditions_item = _parse_conditions_item(conditions_item_data)

                conditions.append(conditions_item)

        _on_condition_failure = d.pop("on_condition_failure", UNSET)
        on_condition_failure: ManagedRealtimeScheduleRequestOnConditionFailure | Unset
        if isinstance(_on_condition_failure, Unset):
            on_condition_failure = UNSET
        else:
            on_condition_failure = check_managed_realtime_schedule_request_on_condition_failure(_on_condition_failure)

        interval_seconds = d.pop("interval_seconds", UNSET)

        max_occurrences = d.pop("max_occurrences", UNSET)

        _end_at = d.pop("end_at", UNSET)
        end_at: datetime.datetime | Unset
        if isinstance(_end_at, Unset):
            end_at = UNSET
        else:
            end_at = datetime.datetime.fromisoformat(_end_at)

        binary = d.pop("binary", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: ManagedRealtimeScheduleRequestMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ManagedRealtimeScheduleRequestMetadata.from_dict(_metadata)

        skip_reason = d.pop("skip_reason", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _last_attempt_at = d.pop("last_attempt_at", UNSET)
        last_attempt_at: datetime.datetime | Unset
        if isinstance(_last_attempt_at, Unset):
            last_attempt_at = UNSET
        else:
            last_attempt_at = datetime.datetime.fromisoformat(_last_attempt_at)

        sequence = d.pop("sequence", UNSET)

        last_error = d.pop("last_error", UNSET)

        managed_realtime_schedule_response = cls(
            data_base64=data_base64,
            deliver_at=deliver_at,
            max_attempts=max_attempts,
            backoff_seconds=backoff_seconds,
            initial_deliver_at=initial_deliver_at,
            skipped_occurrences=skipped_occurrences,
            occurrence=occurrence,
            completed_occurrences=completed_occurrences,
            attempts=attempts,
            cycle_attempts=cycle_attempts,
            schedule_id=schedule_id,
            channel=channel,
            version=version,
            status=status,
            created_at=created_at,
            updated_at=updated_at,
            group=group,
            conditions=conditions,
            on_condition_failure=on_condition_failure,
            interval_seconds=interval_seconds,
            max_occurrences=max_occurrences,
            end_at=end_at,
            binary=binary,
            metadata=metadata,
            skip_reason=skip_reason,
            next_attempt_at=next_attempt_at,
            last_attempt_at=last_attempt_at,
            sequence=sequence,
            last_error=last_error,
        )

        managed_realtime_schedule_response.additional_properties = d
        return managed_realtime_schedule_response

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
