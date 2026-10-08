from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_dependency_finding import OperationWorkflowDependencyFinding

T = TypeVar("T", bound="OperationWorkflowDependencyTrace")

@define
class OperationWorkflowDependencyTrace:
    findings: list[OperationWorkflowDependencyFinding]
    visited_workflow_count: int
    examined_dependency_count: int
    depth_limit: int
    workflow_limit: int
    finding_limit: int
    dependency_limit: int
    truncated: bool
    limits_reached: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        result = {"findings": [v.to_dict() for v in self.findings], "visited_workflow_count": self.visited_workflow_count,
                  "examined_dependency_count": self.examined_dependency_count, "depth_limit": self.depth_limit,
                  "workflow_limit": self.workflow_limit, "finding_limit": self.finding_limit,
                  "dependency_limit": self.dependency_limit, "truncated": self.truncated}
        if self.limits_reached is not UNSET:
            result["limits_reached"] = self.limits_reached
        return result

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(findings=[OperationWorkflowDependencyFinding.from_dict(v) for v in src_dict["findings"]],
                   visited_workflow_count=src_dict["visited_workflow_count"], examined_dependency_count=src_dict["examined_dependency_count"],
                   depth_limit=src_dict["depth_limit"], workflow_limit=src_dict["workflow_limit"],
                   finding_limit=src_dict["finding_limit"], dependency_limit=src_dict["dependency_limit"],
                   truncated=src_dict["truncated"], limits_reached=list(src_dict["limits_reached"]) if "limits_reached" in src_dict else UNSET)
