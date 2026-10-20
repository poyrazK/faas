from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.tenant_workflow_schedule_response import TenantWorkflowScheduleResponse


T = TypeVar("T", bound="ListTenantWorkflowSchedulesResponse")


@_attrs_define
class ListTenantWorkflowSchedulesResponse:
    """Schedule triggers that the app owner opted in to tenant-specific configuration."""

    schedules: list[TenantWorkflowScheduleResponse]

    def to_dict(self) -> dict[str, Any]:
        schedules = []
        for schedules_item_data in self.schedules:
            schedules_item = schedules_item_data.to_dict()
            schedules.append(schedules_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "schedules": schedules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tenant_workflow_schedule_response import TenantWorkflowScheduleResponse

        d = dict(src_dict)
        schedules = []
        _schedules = d.pop("schedules")
        for schedules_item_data in _schedules:
            schedules_item = TenantWorkflowScheduleResponse.from_dict(schedules_item_data)

            schedules.append(schedules_item)

        list_tenant_workflow_schedules_response = cls(
            schedules=schedules,
        )

        return list_tenant_workflow_schedules_response
