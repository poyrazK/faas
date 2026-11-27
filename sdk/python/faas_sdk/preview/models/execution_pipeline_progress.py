from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_progress_status import (
    ExecutionPipelineProgressStatus,
    check_execution_pipeline_progress_status,
)

if TYPE_CHECKING:
    from ..models.execution_pipeline_step_progress import ExecutionPipelineStepProgress


T = TypeVar("T", bound="ExecutionPipelineProgress")


@_attrs_define
class ExecutionPipelineProgress:
    """Current pipeline sequence and observed progress for each step."""

    status: ExecutionPipelineProgressStatus
    steps: list[ExecutionPipelineStepProgress]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "steps": steps,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_step_progress import ExecutionPipelineStepProgress

        d = dict(src_dict)
        status = check_execution_pipeline_progress_status(d.pop("status"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = ExecutionPipelineStepProgress.from_dict(steps_item_data)

            steps.append(steps_item)

        execution_pipeline_progress = cls(
            status=status,
            steps=steps,
        )

        execution_pipeline_progress.additional_properties = d
        return execution_pipeline_progress

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
