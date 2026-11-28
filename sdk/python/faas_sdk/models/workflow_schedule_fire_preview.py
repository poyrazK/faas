from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_schedule_fire_preview_dst_adjustment import (
    WorkflowScheduleFirePreviewDstAdjustment,
    check_workflow_schedule_fire_preview_dst_adjustment,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowScheduleFirePreview")


@_attrs_define
class WorkflowScheduleFirePreview:
    """One canonical fire time with its offset-bearing local representation."""

    scheduled_for: datetime.datetime
    """UTC instant used by the scheduler."""
    local_time: datetime.datetime
    """Local wall time including the UTC offset."""
    dst_adjustment: WorkflowScheduleFirePreviewDstAdjustment | Unset = UNSET
    """Present when a spring gap moves the configured wall time to the first valid minute."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scheduled_for = self.scheduled_for.isoformat()

        local_time = self.local_time.isoformat()

        dst_adjustment: str | Unset = UNSET
        if not isinstance(self.dst_adjustment, Unset):
            dst_adjustment = self.dst_adjustment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scheduled_for": scheduled_for,
                "local_time": local_time,
            }
        )
        if dst_adjustment is not UNSET:
            field_dict["dst_adjustment"] = dst_adjustment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scheduled_for = datetime.datetime.fromisoformat(d.pop("scheduled_for"))

        local_time = datetime.datetime.fromisoformat(d.pop("local_time"))

        _dst_adjustment = d.pop("dst_adjustment", UNSET)
        dst_adjustment: WorkflowScheduleFirePreviewDstAdjustment | Unset
        if isinstance(_dst_adjustment, Unset):
            dst_adjustment = UNSET
        else:
            dst_adjustment = check_workflow_schedule_fire_preview_dst_adjustment(_dst_adjustment)

        workflow_schedule_fire_preview = cls(
            scheduled_for=scheduled_for,
            local_time=local_time,
            dst_adjustment=dst_adjustment,
        )

        workflow_schedule_fire_preview.additional_properties = d
        return workflow_schedule_fire_preview

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
