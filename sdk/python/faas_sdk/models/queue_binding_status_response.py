from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_binding_status_response_consumer_liveness import (
    QueueBindingStatusResponseConsumerLiveness,
    check_queue_binding_status_response_consumer_liveness,
)
from ..models.queue_binding_status_response_consumer_state import (
    QueueBindingStatusResponseConsumerState,
    check_queue_binding_status_response_consumer_state,
)
from ..models.queue_binding_status_response_mode import (
    QueueBindingStatusResponseMode,
    check_queue_binding_status_response_mode,
)
from ..models.queue_binding_status_response_workload_class import (
    QueueBindingStatusResponseWorkloadClass,
    check_queue_binding_status_response_workload_class,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="QueueBindingStatusResponse")


@_attrs_define
class QueueBindingStatusResponse:
    """Read-only queue binding consumer projection and queue counters."""

    binding_id: str
    name: str
    queue_name: str
    mode: QueueBindingStatusResponseMode
    workload_class: QueueBindingStatusResponseWorkloadClass
    enabled: bool
    consumer_state: QueueBindingStatusResponseConsumerState
    consumer_liveness: QueueBindingStatusResponseConsumerLiveness
    depth: int
    in_flight: int
    dead_letter: int
    generated_at: datetime.datetime
    consumer_state_reason: str | Unset = UNSET
    trigger_id: str | Unset = UNSET
    last_poll_at: datetime.datetime | None | Unset = UNSET
    last_success_at: datetime.datetime | None | Unset = UNSET
    last_error_at: datetime.datetime | None | Unset = UNSET
    last_error: str | Unset = UNSET
    lag_messages: int | None | Unset = UNSET
    lag_age_seconds: float | None | Unset = UNSET
    oldest_pending_at: datetime.datetime | None | Unset = UNSET
    oldest_pending_age_seconds: int | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        binding_id = self.binding_id

        name = self.name

        queue_name = self.queue_name

        mode: str = self.mode

        workload_class: str = self.workload_class

        enabled = self.enabled

        consumer_state: str = self.consumer_state

        consumer_liveness: str = self.consumer_liveness

        depth = self.depth

        in_flight = self.in_flight

        dead_letter = self.dead_letter

        generated_at = self.generated_at.isoformat()

        consumer_state_reason = self.consumer_state_reason

        trigger_id = self.trigger_id

        last_poll_at: None | str | Unset
        if isinstance(self.last_poll_at, Unset):
            last_poll_at = UNSET
        elif isinstance(self.last_poll_at, datetime.datetime):
            last_poll_at = self.last_poll_at.isoformat()
        else:
            last_poll_at = self.last_poll_at

        last_success_at: None | str | Unset
        if isinstance(self.last_success_at, Unset):
            last_success_at = UNSET
        elif isinstance(self.last_success_at, datetime.datetime):
            last_success_at = self.last_success_at.isoformat()
        else:
            last_success_at = self.last_success_at

        last_error_at: None | str | Unset
        if isinstance(self.last_error_at, Unset):
            last_error_at = UNSET
        elif isinstance(self.last_error_at, datetime.datetime):
            last_error_at = self.last_error_at.isoformat()
        else:
            last_error_at = self.last_error_at

        last_error = self.last_error

        lag_messages: int | None | Unset
        if isinstance(self.lag_messages, Unset):
            lag_messages = UNSET
        else:
            lag_messages = self.lag_messages

        lag_age_seconds: float | None | Unset
        if isinstance(self.lag_age_seconds, Unset):
            lag_age_seconds = UNSET
        else:
            lag_age_seconds = self.lag_age_seconds

        oldest_pending_at: None | str | Unset
        if isinstance(self.oldest_pending_at, Unset):
            oldest_pending_at = UNSET
        elif isinstance(self.oldest_pending_at, datetime.datetime):
            oldest_pending_at = self.oldest_pending_at.isoformat()
        else:
            oldest_pending_at = self.oldest_pending_at

        oldest_pending_age_seconds: int | None | Unset
        if isinstance(self.oldest_pending_age_seconds, Unset):
            oldest_pending_age_seconds = UNSET
        else:
            oldest_pending_age_seconds = self.oldest_pending_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "binding_id": binding_id,
                "name": name,
                "queue_name": queue_name,
                "mode": mode,
                "workload_class": workload_class,
                "enabled": enabled,
                "consumer_state": consumer_state,
                "consumer_liveness": consumer_liveness,
                "depth": depth,
                "in_flight": in_flight,
                "dead_letter": dead_letter,
                "generated_at": generated_at,
            }
        )
        if consumer_state_reason is not UNSET:
            field_dict["consumer_state_reason"] = consumer_state_reason
        if trigger_id is not UNSET:
            field_dict["trigger_id"] = trigger_id
        if last_poll_at is not UNSET:
            field_dict["last_poll_at"] = last_poll_at
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if last_error_at is not UNSET:
            field_dict["last_error_at"] = last_error_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if lag_messages is not UNSET:
            field_dict["lag_messages"] = lag_messages
        if lag_age_seconds is not UNSET:
            field_dict["lag_age_seconds"] = lag_age_seconds
        if oldest_pending_at is not UNSET:
            field_dict["oldest_pending_at"] = oldest_pending_at
        if oldest_pending_age_seconds is not UNSET:
            field_dict["oldest_pending_age_seconds"] = oldest_pending_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        binding_id = d.pop("binding_id")

        name = d.pop("name")

        queue_name = d.pop("queue_name")

        mode = check_queue_binding_status_response_mode(d.pop("mode"))

        workload_class = check_queue_binding_status_response_workload_class(d.pop("workload_class"))

        enabled = d.pop("enabled")

        consumer_state = check_queue_binding_status_response_consumer_state(d.pop("consumer_state"))

        consumer_liveness = check_queue_binding_status_response_consumer_liveness(d.pop("consumer_liveness"))

        depth = d.pop("depth")

        in_flight = d.pop("in_flight")

        dead_letter = d.pop("dead_letter")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        consumer_state_reason = d.pop("consumer_state_reason", UNSET)

        trigger_id = d.pop("trigger_id", UNSET)

        def _parse_last_poll_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_poll_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_poll_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_poll_at = _parse_last_poll_at(d.pop("last_poll_at", UNSET))

        def _parse_last_success_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_success_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_success_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_success_at = _parse_last_success_at(d.pop("last_success_at", UNSET))

        def _parse_last_error_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_error_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_error_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_error_at = _parse_last_error_at(d.pop("last_error_at", UNSET))

        last_error = d.pop("last_error", UNSET)

        def _parse_lag_messages(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        lag_messages = _parse_lag_messages(d.pop("lag_messages", UNSET))

        def _parse_lag_age_seconds(data: object) -> float | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(float | None | Unset, data)

        lag_age_seconds = _parse_lag_age_seconds(d.pop("lag_age_seconds", UNSET))

        def _parse_oldest_pending_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                oldest_pending_at_type_0 = datetime.datetime.fromisoformat(data)

                return oldest_pending_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        oldest_pending_at = _parse_oldest_pending_at(d.pop("oldest_pending_at", UNSET))

        def _parse_oldest_pending_age_seconds(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        oldest_pending_age_seconds = _parse_oldest_pending_age_seconds(d.pop("oldest_pending_age_seconds", UNSET))

        queue_binding_status_response = cls(
            binding_id=binding_id,
            name=name,
            queue_name=queue_name,
            mode=mode,
            workload_class=workload_class,
            enabled=enabled,
            consumer_state=consumer_state,
            consumer_liveness=consumer_liveness,
            depth=depth,
            in_flight=in_flight,
            dead_letter=dead_letter,
            generated_at=generated_at,
            consumer_state_reason=consumer_state_reason,
            trigger_id=trigger_id,
            last_poll_at=last_poll_at,
            last_success_at=last_success_at,
            last_error_at=last_error_at,
            last_error=last_error,
            lag_messages=lag_messages,
            lag_age_seconds=lag_age_seconds,
            oldest_pending_at=oldest_pending_at,
            oldest_pending_age_seconds=oldest_pending_age_seconds,
        )

        queue_binding_status_response.additional_properties = d
        return queue_binding_status_response

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
