from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneInvalidationRule")


@_attrs_define
class OperationWorkflowRetainedMilestoneInvalidationRule:
    """Versioned link between a declared retained prerequisite and a distinct invalidating source bound to a transaction-
    backed workflow step. Sources must share ownership, subject, workflow instance, and contract version.

    """

    prerequisite: OperationWorkflowRetainedMilestoneRequirement
    """Source operation and milestone bound to a declared transaction-backed step of the selected workflow
    contract."""
    invalidated_by: OperationWorkflowRetainedMilestoneRequirement
    """Source operation and milestone bound to a declared transaction-backed step of the selected workflow
    contract."""

    def to_dict(self) -> dict[str, Any]:
        prerequisite = self.prerequisite.to_dict()

        invalidated_by = self.invalidated_by.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "prerequisite": prerequisite,
                "invalidated_by": invalidated_by,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        prerequisite = OperationWorkflowRetainedMilestoneRequirement.from_dict(d.pop("prerequisite"))

        invalidated_by = OperationWorkflowRetainedMilestoneRequirement.from_dict(d.pop("invalidated_by"))

        operation_workflow_retained_milestone_invalidation_rule = cls(
            prerequisite=prerequisite,
            invalidated_by=invalidated_by,
        )

        return operation_workflow_retained_milestone_invalidation_rule
