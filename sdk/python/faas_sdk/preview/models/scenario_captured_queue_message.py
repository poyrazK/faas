from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

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
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workload = self.workload

        kind: str = self.kind

        target = self.target

        message = self.message

        queue_name = self.queue_name

        schedule = self.schedule

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

        scenario_captured_queue_message = cls(
            id=id,
            workload=workload,
            kind=kind,
            target=target,
            message=message,
            queue_name=queue_name,
            schedule=schedule,
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
