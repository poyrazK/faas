from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_schedule_dst_behavior_fall_fold import (
    WorkflowScheduleDSTBehaviorFallFold,
    check_workflow_schedule_dst_behavior_fall_fold,
)
from ..models.workflow_schedule_dst_behavior_mode import (
    WorkflowScheduleDSTBehaviorMode,
    check_workflow_schedule_dst_behavior_mode,
)
from ..models.workflow_schedule_dst_behavior_spring_gap import (
    WorkflowScheduleDSTBehaviorSpringGap,
    check_workflow_schedule_dst_behavior_spring_gap,
)

T = TypeVar("T", bound="WorkflowScheduleDSTBehavior")


@_attrs_define
class WorkflowScheduleDSTBehavior:
    """Gregale's timezone-transition behavior for fixed wall-time and interval cron expressions."""

    mode: WorkflowScheduleDSTBehaviorMode
    spring_gap: WorkflowScheduleDSTBehaviorSpringGap
    fall_fold: WorkflowScheduleDSTBehaviorFallFold
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        spring_gap: str = self.spring_gap

        fall_fold: str = self.fall_fold

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "spring_gap": spring_gap,
                "fall_fold": fall_fold,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_workflow_schedule_dst_behavior_mode(d.pop("mode"))

        spring_gap = check_workflow_schedule_dst_behavior_spring_gap(d.pop("spring_gap"))

        fall_fold = check_workflow_schedule_dst_behavior_fall_fold(d.pop("fall_fold"))

        workflow_schedule_dst_behavior = cls(
            mode=mode,
            spring_gap=spring_gap,
            fall_fold=fall_fold,
        )

        workflow_schedule_dst_behavior.additional_properties = d
        return workflow_schedule_dst_behavior

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
