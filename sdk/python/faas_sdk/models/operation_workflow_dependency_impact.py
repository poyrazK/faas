from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from .operation_workflow_dependent_instance import OperationWorkflowDependentInstance

T = TypeVar("T", bound="OperationWorkflowDependencyImpact")

@define
class OperationWorkflowDependencyImpact:
    items: list[OperationWorkflowDependentInstance]
    workflow_count: int
    impacted_workflow_count: int
    has_more: bool

    def to_dict(self) -> dict[str, Any]:
        return {"items": [item.to_dict() for item in self.items], "workflow_count": self.workflow_count,
                "impacted_workflow_count": self.impacted_workflow_count, "has_more": self.has_more}

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(items=[OperationWorkflowDependentInstance.from_dict(v) for v in src_dict["items"]],
                   workflow_count=src_dict["workflow_count"], impacted_workflow_count=src_dict["impacted_workflow_count"],
                   has_more=src_dict["has_more"])
