from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.execution_pipeline_reused_step import ExecutionPipelineReusedStep


T = TypeVar("T", bound="ExecutionPipelineRerun")


@_attrs_define
class ExecutionPipelineRerun:
    """Rerun receipt linking the replacement pipeline to its original job and reused outputs."""

    parent_id: UUID
    parent_workflow_id: str
    parent_plan_id: str
    reused: list[ExecutionPipelineReusedStep]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        parent_id = str(self.parent_id)

        parent_workflow_id = self.parent_workflow_id

        parent_plan_id = self.parent_plan_id

        reused = []
        for reused_item_data in self.reused:
            reused_item = reused_item_data.to_dict()
            reused.append(reused_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "parent_id": parent_id,
                "parent_workflow_id": parent_workflow_id,
                "parent_plan_id": parent_plan_id,
                "reused": reused,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_reused_step import ExecutionPipelineReusedStep

        d = dict(src_dict)
        parent_id = UUID(d.pop("parent_id"))

        parent_workflow_id = d.pop("parent_workflow_id")

        parent_plan_id = d.pop("parent_plan_id")

        reused = []
        _reused = d.pop("reused")
        for reused_item_data in _reused:
            reused_item = ExecutionPipelineReusedStep.from_dict(reused_item_data)

            reused.append(reused_item)

        execution_pipeline_rerun = cls(
            parent_id=parent_id,
            parent_workflow_id=parent_workflow_id,
            parent_plan_id=parent_plan_id,
            reused=reused,
        )

        execution_pipeline_rerun.additional_properties = d
        return execution_pipeline_rerun

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
