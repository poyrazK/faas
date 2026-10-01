from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.execution_workflow_status_counts import ExecutionWorkflowStatusCounts
    from ..models.execution_workflow_usage import ExecutionWorkflowUsage


T = TypeVar("T", bound="ExecutionWorkflowResponse")


@_attrs_define
class ExecutionWorkflowResponse:
    """Workflow lifecycle and usage aggregate visible to the authenticated principal."""

    workflow_id: str
    run_count: int
    status_counts: ExecutionWorkflowStatusCounts
    """Number of runs in each lifecycle state."""
    usage: ExecutionWorkflowUsage
    """Host-measured usage summed over terminal runs; peak memory is the maximum individual run peak."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workflow_id = self.workflow_id

        run_count = self.run_count

        status_counts = self.status_counts.to_dict()

        usage = self.usage.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workflow_id": workflow_id,
                "run_count": run_count,
                "status_counts": status_counts,
                "usage": usage,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_workflow_status_counts import ExecutionWorkflowStatusCounts
        from ..models.execution_workflow_usage import ExecutionWorkflowUsage

        d = dict(src_dict)
        workflow_id = d.pop("workflow_id")

        run_count = d.pop("run_count")

        status_counts = ExecutionWorkflowStatusCounts.from_dict(d.pop("status_counts"))

        usage = ExecutionWorkflowUsage.from_dict(d.pop("usage"))

        execution_workflow_response = cls(
            workflow_id=workflow_id,
            run_count=run_count,
            status_counts=status_counts,
            usage=usage,
        )

        execution_workflow_response.additional_properties = d
        return execution_workflow_response

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
