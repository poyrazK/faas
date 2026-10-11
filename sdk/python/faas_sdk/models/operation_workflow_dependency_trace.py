from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_dependency_trace_dependency_limit import (
    OperationWorkflowDependencyTraceDependencyLimit,
    check_operation_workflow_dependency_trace_dependency_limit,
)
from ..models.operation_workflow_dependency_trace_depth_limit import (
    OperationWorkflowDependencyTraceDepthLimit,
    check_operation_workflow_dependency_trace_depth_limit,
)
from ..models.operation_workflow_dependency_trace_finding_limit import (
    OperationWorkflowDependencyTraceFindingLimit,
    check_operation_workflow_dependency_trace_finding_limit,
)
from ..models.operation_workflow_dependency_trace_limits_reached_item import (
    OperationWorkflowDependencyTraceLimitsReachedItem,
    check_operation_workflow_dependency_trace_limits_reached_item,
)
from ..models.operation_workflow_dependency_trace_workflow_limit import (
    OperationWorkflowDependencyTraceWorkflowLimit,
    check_operation_workflow_dependency_trace_workflow_limit,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_dependency_finding import OperationWorkflowDependencyFinding


T = TypeVar("T", bound="OperationWorkflowDependencyTrace")


@_attrs_define
class OperationWorkflowDependencyTrace:
    """Bounded deterministic depth-first traversal of unmet reported prerequisites within one
    customer/application/environment. Unknown retained state and traversal limits are distinct. Terminal sources and
    satisfied requirements stop traversal. Current reports may change while reading.

    """

    findings: list[OperationWorkflowDependencyFinding]
    visited_workflow_count: int
    """Distinct inspected workflow references including the selected source and cached lookups of unknown or
    satisfied prerequisites."""
    examined_dependency_count: int
    depth_limit: OperationWorkflowDependencyTraceDepthLimit
    workflow_limit: OperationWorkflowDependencyTraceWorkflowLimit
    finding_limit: OperationWorkflowDependencyTraceFindingLimit
    dependency_limit: OperationWorkflowDependencyTraceDependencyLimit
    truncated: bool
    limits_reached: list[OperationWorkflowDependencyTraceLimitsReachedItem] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        findings = []
        for findings_item_data in self.findings:
            findings_item = findings_item_data.to_dict()
            findings.append(findings_item)

        visited_workflow_count = self.visited_workflow_count

        examined_dependency_count = self.examined_dependency_count

        depth_limit: int = self.depth_limit

        workflow_limit: int = self.workflow_limit

        finding_limit: int = self.finding_limit

        dependency_limit: int = self.dependency_limit

        truncated = self.truncated

        limits_reached: list[str] | Unset = UNSET
        if not isinstance(self.limits_reached, Unset):
            limits_reached = []
            for limits_reached_item_data in self.limits_reached:
                limits_reached_item: str = limits_reached_item_data
                limits_reached.append(limits_reached_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "findings": findings,
                "visited_workflow_count": visited_workflow_count,
                "examined_dependency_count": examined_dependency_count,
                "depth_limit": depth_limit,
                "workflow_limit": workflow_limit,
                "finding_limit": finding_limit,
                "dependency_limit": dependency_limit,
                "truncated": truncated,
            }
        )
        if limits_reached is not UNSET:
            field_dict["limits_reached"] = limits_reached

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_dependency_finding import OperationWorkflowDependencyFinding

        d = dict(src_dict)
        findings = []
        _findings = d.pop("findings")
        for findings_item_data in _findings:
            findings_item = OperationWorkflowDependencyFinding.from_dict(findings_item_data)

            findings.append(findings_item)

        visited_workflow_count = d.pop("visited_workflow_count")

        examined_dependency_count = d.pop("examined_dependency_count")

        depth_limit = check_operation_workflow_dependency_trace_depth_limit(d.pop("depth_limit"))

        workflow_limit = check_operation_workflow_dependency_trace_workflow_limit(d.pop("workflow_limit"))

        finding_limit = check_operation_workflow_dependency_trace_finding_limit(d.pop("finding_limit"))

        dependency_limit = check_operation_workflow_dependency_trace_dependency_limit(d.pop("dependency_limit"))

        truncated = d.pop("truncated")

        _limits_reached = d.pop("limits_reached", UNSET)
        limits_reached: list[OperationWorkflowDependencyTraceLimitsReachedItem] | Unset = UNSET
        if _limits_reached is not UNSET:
            limits_reached = []
            for limits_reached_item_data in _limits_reached:
                limits_reached_item = check_operation_workflow_dependency_trace_limits_reached_item(
                    limits_reached_item_data
                )

                limits_reached.append(limits_reached_item)

        operation_workflow_dependency_trace = cls(
            findings=findings,
            visited_workflow_count=visited_workflow_count,
            examined_dependency_count=examined_dependency_count,
            depth_limit=depth_limit,
            workflow_limit=workflow_limit,
            finding_limit=finding_limit,
            dependency_limit=dependency_limit,
            truncated=truncated,
            limits_reached=limits_reached,
        )

        return operation_workflow_dependency_trace
