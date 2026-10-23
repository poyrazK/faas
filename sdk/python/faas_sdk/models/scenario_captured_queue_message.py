from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_captured_queue_message_delivery import (
    ScenarioCapturedQueueMessageDelivery,
    check_scenario_captured_queue_message_delivery,
)
from ..models.scenario_captured_queue_message_kind import (
    ScenarioCapturedQueueMessageKind,
    check_scenario_captured_queue_message_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioCapturedQueueMessage")


@_attrs_define
class ScenarioCapturedQueueMessage:
    """A queue send, inbox message, delayed task or asynchronous invoke an enrolled app requested through the run-local
    platform API. It was never enqueued (ADR-20261021090000, ADR-20261022082721).

    """

    id: UUID
    workload: str
    kind: ScenarioCapturedQueueMessageKind
    target: str
    """A member of the same run is named workload:<name>; any other app is app:<slug>."""
    message: Any
    """The request's business content, bounded to 64 KiB."""
    queue_name: str | Unset = UNSET
    schedule: str | Unset = UNSET
    """A delayed task's requested delay, or absolute when it gave scheduled_at."""
    delivery: ScenarioCapturedQueueMessageDelivery | Unset = UNSET
    """In a run with queue delivery, a send addressed to a member: delivered as a real invocation, unsupported, or
    over the delivery bound (ADR-20261023111825)."""
    delivery_state: str | Unset = UNSET
    """A delivered send's invocation state when the run sealed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workload = self.workload

        kind: str = self.kind

        target = self.target

        message = self.message

        queue_name = self.queue_name

        schedule = self.schedule

        delivery: str | Unset = UNSET
        if not isinstance(self.delivery, Unset):
            delivery = self.delivery

        delivery_state = self.delivery_state

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "workload": workload,
                "kind": kind,
                "target": target,
                "message": message,
            }
        )
        if queue_name is not UNSET:
            field_dict["queue_name"] = queue_name
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if delivery is not UNSET:
            field_dict["delivery"] = delivery
        if delivery_state is not UNSET:
            field_dict["delivery_state"] = delivery_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workload = d.pop("workload")

        kind = check_scenario_captured_queue_message_kind(d.pop("kind"))

        target = d.pop("target")

        message = d.pop("message")

        queue_name = d.pop("queue_name", UNSET)

        schedule = d.pop("schedule", UNSET)

        _delivery = d.pop("delivery", UNSET)
        delivery: ScenarioCapturedQueueMessageDelivery | Unset
        if isinstance(_delivery, Unset):
            delivery = UNSET
        else:
            delivery = check_scenario_captured_queue_message_delivery(_delivery)

        delivery_state = d.pop("delivery_state", UNSET)

        scenario_captured_queue_message = cls(
            id=id,
            workload=workload,
            kind=kind,
            target=target,
            message=message,
            queue_name=queue_name,
            schedule=schedule,
            delivery=delivery,
            delivery_state=delivery_state,
        )

        scenario_captured_queue_message.additional_properties = d
        return scenario_captured_queue_message

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
