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
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_schedule_request_metadata import ManagedRealtimeScheduleRequestMetadata


T = TypeVar("T", bound="ManagedRealtimeScheduleRequest")


@_attrs_define
class ManagedRealtimeScheduleRequest:
    """Scheduled channel publish payload with recurrence and retry settings."""

    data_base64: str
    """At most 4096 decoded bytes."""
    deliver_at: datetime.datetime
    """Future time within 30 days of creation."""
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
    max_attempts: int | Unset = 1
    """Total attempts per retry cycle; one disables automatic retries."""
    backoff_seconds: int | Unset = 5
    """Initial retry delay, doubled per failure up to one hour."""
    binary: bool | Unset = False
    metadata: ManagedRealtimeScheduleRequestMetadata | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_base64 = self.data_base64

        deliver_at = self.deliver_at.isoformat()

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

        max_attempts = self.max_attempts

        backoff_seconds = self.backoff_seconds

        binary = self.binary

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_base64": data_base64,
                "deliver_at": deliver_at,
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
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if backoff_seconds is not UNSET:
            field_dict["backoff_seconds"] = backoff_seconds
        if binary is not UNSET:
            field_dict["binary"] = binary
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_schedule_request_metadata import ManagedRealtimeScheduleRequestMetadata

        d = dict(src_dict)
        data_base64 = d.pop("data_base64")

        deliver_at = datetime.datetime.fromisoformat(d.pop("deliver_at"))

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

        max_attempts = d.pop("max_attempts", UNSET)

        backoff_seconds = d.pop("backoff_seconds", UNSET)

        binary = d.pop("binary", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: ManagedRealtimeScheduleRequestMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ManagedRealtimeScheduleRequestMetadata.from_dict(_metadata)

        managed_realtime_schedule_request = cls(
            data_base64=data_base64,
            deliver_at=deliver_at,
            group=group,
            conditions=conditions,
            on_condition_failure=on_condition_failure,
            interval_seconds=interval_seconds,
            max_occurrences=max_occurrences,
            end_at=end_at,
            max_attempts=max_attempts,
            backoff_seconds=backoff_seconds,
            binary=binary,
            metadata=metadata,
        )

        managed_realtime_schedule_request.additional_properties = d
        return managed_realtime_schedule_request

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
