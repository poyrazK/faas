from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_schedule_response_last_status import (
    WorkflowScheduleResponseLastStatus,
    check_workflow_schedule_response_last_status,
)
from ..models.workflow_schedule_response_overlap import (
    WorkflowScheduleResponseOverlap,
    check_workflow_schedule_response_overlap,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowScheduleResponse")


@_attrs_define
class WorkflowScheduleResponse:
    """Current deployed schedule configuration and its latest durable admission outcome."""

    workflow_name: str
    deployment_id: UUID
    schedule: str
    timezone: str
    overlap: WorkflowScheduleResponseOverlap
    enabled: bool
    next_fire_at: datetime.datetime | Unset = UNSET
    last_evaluated_at: datetime.datetime | Unset = UNSET
    last_scheduled_for: datetime.datetime | Unset = UNSET
    last_status: WorkflowScheduleResponseLastStatus | Unset = UNSET
    last_run_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workflow_name = self.workflow_name

        deployment_id = str(self.deployment_id)

        schedule = self.schedule

        timezone = self.timezone

        overlap: str = self.overlap

        enabled = self.enabled

        next_fire_at: str | Unset = UNSET
        if not isinstance(self.next_fire_at, Unset):
            next_fire_at = self.next_fire_at.isoformat()

        last_evaluated_at: str | Unset = UNSET
        if not isinstance(self.last_evaluated_at, Unset):
            last_evaluated_at = self.last_evaluated_at.isoformat()

        last_scheduled_for: str | Unset = UNSET
        if not isinstance(self.last_scheduled_for, Unset):
            last_scheduled_for = self.last_scheduled_for.isoformat()

        last_status: str | Unset = UNSET
        if not isinstance(self.last_status, Unset):
            last_status = self.last_status

        last_run_id: str | Unset = UNSET
        if not isinstance(self.last_run_id, Unset):
            last_run_id = str(self.last_run_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workflow_name": workflow_name,
                "deployment_id": deployment_id,
                "schedule": schedule,
                "timezone": timezone,
                "overlap": overlap,
                "enabled": enabled,
            }
        )
        if next_fire_at is not UNSET:
            field_dict["next_fire_at"] = next_fire_at
        if last_evaluated_at is not UNSET:
            field_dict["last_evaluated_at"] = last_evaluated_at
        if last_scheduled_for is not UNSET:
            field_dict["last_scheduled_for"] = last_scheduled_for
        if last_status is not UNSET:
            field_dict["last_status"] = last_status
        if last_run_id is not UNSET:
            field_dict["last_run_id"] = last_run_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow_name = d.pop("workflow_name")

        deployment_id = UUID(d.pop("deployment_id"))

        schedule = d.pop("schedule")

        timezone = d.pop("timezone")

        overlap = check_workflow_schedule_response_overlap(d.pop("overlap"))

        enabled = d.pop("enabled")

        _next_fire_at = d.pop("next_fire_at", UNSET)
        next_fire_at: datetime.datetime | Unset
        if isinstance(_next_fire_at, Unset):
            next_fire_at = UNSET
        else:
            next_fire_at = datetime.datetime.fromisoformat(_next_fire_at)

        _last_evaluated_at = d.pop("last_evaluated_at", UNSET)
        last_evaluated_at: datetime.datetime | Unset
        if isinstance(_last_evaluated_at, Unset):
            last_evaluated_at = UNSET
        else:
            last_evaluated_at = datetime.datetime.fromisoformat(_last_evaluated_at)

        _last_scheduled_for = d.pop("last_scheduled_for", UNSET)
        last_scheduled_for: datetime.datetime | Unset
        if isinstance(_last_scheduled_for, Unset):
            last_scheduled_for = UNSET
        else:
            last_scheduled_for = datetime.datetime.fromisoformat(_last_scheduled_for)

        _last_status = d.pop("last_status", UNSET)
        last_status: WorkflowScheduleResponseLastStatus | Unset
        if isinstance(_last_status, Unset):
            last_status = UNSET
        else:
            last_status = check_workflow_schedule_response_last_status(_last_status)

        _last_run_id = d.pop("last_run_id", UNSET)
        last_run_id: UUID | Unset
        if isinstance(_last_run_id, Unset):
            last_run_id = UNSET
        else:
            last_run_id = UUID(_last_run_id)

        workflow_schedule_response = cls(
            workflow_name=workflow_name,
            deployment_id=deployment_id,
            schedule=schedule,
            timezone=timezone,
            overlap=overlap,
            enabled=enabled,
            next_fire_at=next_fire_at,
            last_evaluated_at=last_evaluated_at,
            last_scheduled_for=last_scheduled_for,
            last_status=last_status,
            last_run_id=last_run_id,
        )

        workflow_schedule_response.additional_properties = d
        return workflow_schedule_response

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
