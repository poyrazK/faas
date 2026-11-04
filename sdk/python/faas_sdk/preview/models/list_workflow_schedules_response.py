from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.list_workflow_schedules_response_unavailable_reason import (
    ListWorkflowSchedulesResponseUnavailableReason,
    check_list_workflow_schedules_response_unavailable_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_schedule_response import WorkflowScheduleResponse


T = TypeVar("T", bound="ListWorkflowSchedulesResponse")


@_attrs_define
class ListWorkflowSchedulesResponse:
    """Deployed workflow schedules with runtime availability and any admission blocker."""

    runtime_enabled: bool
    schedules: list[WorkflowScheduleResponse]
    unavailable_reason: ListWorkflowSchedulesResponseUnavailableReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        runtime_enabled = self.runtime_enabled

        schedules = []
        for schedules_item_data in self.schedules:
            schedules_item = schedules_item_data.to_dict()
            schedules.append(schedules_item)

        unavailable_reason: str | Unset = UNSET
        if not isinstance(self.unavailable_reason, Unset):
            unavailable_reason = self.unavailable_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "runtime_enabled": runtime_enabled,
                "schedules": schedules,
            }
        )
        if unavailable_reason is not UNSET:
            field_dict["unavailable_reason"] = unavailable_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_schedule_response import WorkflowScheduleResponse

        d = dict(src_dict)
        runtime_enabled = d.pop("runtime_enabled")

        schedules = []
        _schedules = d.pop("schedules")
        for schedules_item_data in _schedules:
            schedules_item = WorkflowScheduleResponse.from_dict(schedules_item_data)

            schedules.append(schedules_item)

        _unavailable_reason = d.pop("unavailable_reason", UNSET)
        unavailable_reason: ListWorkflowSchedulesResponseUnavailableReason | Unset
        if isinstance(_unavailable_reason, Unset):
            unavailable_reason = UNSET
        else:
            unavailable_reason = check_list_workflow_schedules_response_unavailable_reason(_unavailable_reason)

        list_workflow_schedules_response = cls(
            runtime_enabled=runtime_enabled,
            schedules=schedules,
            unavailable_reason=unavailable_reason,
        )

        list_workflow_schedules_response.additional_properties = d
        return list_workflow_schedules_response

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
