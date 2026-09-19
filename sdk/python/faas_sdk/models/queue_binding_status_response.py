from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

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
    depth: int
    in_flight: int
    dead_letter: int
    generated_at: datetime.datetime
    consumer_state_reason: str | Unset = UNSET
    trigger_id: str | Unset = UNSET
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

        depth = self.depth

        in_flight = self.in_flight

        dead_letter = self.dead_letter

        generated_at = self.generated_at.isoformat()

        consumer_state_reason = self.consumer_state_reason

        trigger_id = self.trigger_id

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

        depth = d.pop("depth")

        in_flight = d.pop("in_flight")

        dead_letter = d.pop("dead_letter")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        consumer_state_reason = d.pop("consumer_state_reason", UNSET)

        trigger_id = d.pop("trigger_id", UNSET)

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
            depth=depth,
            in_flight=in_flight,
            dead_letter=dead_letter,
            generated_at=generated_at,
            consumer_state_reason=consumer_state_reason,
            trigger_id=trigger_id,
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
