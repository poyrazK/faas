from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_running_cause_code import DebugRunningCauseCode, check_debug_running_cause_code
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRunningCause")


@_attrs_define
class DebugRunningCause:
    """One observed scheduler reason an application remained resident. The
    platform reports evidence available at the scheduler boundary; it
    does not infer a protocol or predict a cost saving.

    """

    code: DebugRunningCauseCode
    summary: str
    instance_count: int
    open_connections: int | Unset = UNSET
    tail_tasks: int | Unset = UNSET
    mode: str | Unset = UNSET
    workload_class: str | Unset = UNSET
    last_activity_at: datetime.datetime | Unset = UNSET
    idle_deadline: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        summary = self.summary

        instance_count = self.instance_count

        open_connections = self.open_connections

        tail_tasks = self.tail_tasks

        mode = self.mode

        workload_class = self.workload_class

        last_activity_at: str | Unset = UNSET
        if not isinstance(self.last_activity_at, Unset):
            last_activity_at = self.last_activity_at.isoformat()

        idle_deadline: str | Unset = UNSET
        if not isinstance(self.idle_deadline, Unset):
            idle_deadline = self.idle_deadline.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "summary": summary,
                "instance_count": instance_count,
            }
        )
        if open_connections is not UNSET:
            field_dict["open_connections"] = open_connections
        if tail_tasks is not UNSET:
            field_dict["tail_tasks"] = tail_tasks
        if mode is not UNSET:
            field_dict["mode"] = mode
        if workload_class is not UNSET:
            field_dict["workload_class"] = workload_class
        if last_activity_at is not UNSET:
            field_dict["last_activity_at"] = last_activity_at
        if idle_deadline is not UNSET:
            field_dict["idle_deadline"] = idle_deadline

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = check_debug_running_cause_code(d.pop("code"))

        summary = d.pop("summary")

        instance_count = d.pop("instance_count")

        open_connections = d.pop("open_connections", UNSET)

        tail_tasks = d.pop("tail_tasks", UNSET)

        mode = d.pop("mode", UNSET)

        workload_class = d.pop("workload_class", UNSET)

        _last_activity_at = d.pop("last_activity_at", UNSET)
        last_activity_at: datetime.datetime | Unset
        if isinstance(_last_activity_at, Unset):
            last_activity_at = UNSET
        else:
            last_activity_at = datetime.datetime.fromisoformat(_last_activity_at)

        _idle_deadline = d.pop("idle_deadline", UNSET)
        idle_deadline: datetime.datetime | Unset
        if isinstance(_idle_deadline, Unset):
            idle_deadline = UNSET
        else:
            idle_deadline = datetime.datetime.fromisoformat(_idle_deadline)

        debug_running_cause = cls(
            code=code,
            summary=summary,
            instance_count=instance_count,
            open_connections=open_connections,
            tail_tasks=tail_tasks,
            mode=mode,
            workload_class=workload_class,
            last_activity_at=last_activity_at,
            idle_deadline=idle_deadline,
        )

        debug_running_cause.additional_properties = d
        return debug_running_cause

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
