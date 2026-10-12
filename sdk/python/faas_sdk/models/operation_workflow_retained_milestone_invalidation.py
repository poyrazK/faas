from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneInvalidation")


@_attrs_define
class OperationWorkflowRetainedMilestoneInvalidation:
    """Identity of the retained fact that invalidated a prerequisite by business occurrence time. Equal occurrence
    timestamps invalidate conservatively; a later prerequisite fact can satisfy the requirement again. Normal retention
    applies to both sources.

    """

    source: OperationWorkflowRetainedMilestoneRequirement
    """Source operation and milestone bound to a declared transaction-backed step of the selected workflow
    contract."""
    evidence: OperationWorkflowInstanceMilestoneRef
    """Identity and occurrence and publication times of a milestone observed for a declared workflow step."""

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_dict()

        evidence = self.evidence.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "evidence": evidence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        source = OperationWorkflowRetainedMilestoneRequirement.from_dict(d.pop("source"))

        evidence = OperationWorkflowInstanceMilestoneRef.from_dict(d.pop("evidence"))

        operation_workflow_retained_milestone_invalidation = cls(
            source=source,
            evidence=evidence,
        )

        return operation_workflow_retained_milestone_invalidation
