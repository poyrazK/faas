from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_unmet_invariant_reason import (
    OperationWorkflowUnmetInvariantReason,
    check_operation_workflow_unmet_invariant_reason,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_invariant_requirement import OperationWorkflowInvariantRequirement


T = TypeVar("T", bound="OperationWorkflowUnmetInvariant")


@_attrs_define
class OperationWorkflowUnmetInvariant:
    """An invariant requirement that the proposed evidence cannot satisfy, with its failure reason."""

    requirement: OperationWorkflowInvariantRequirement
    """Exact invariant code and version that must pass in a milestone committed with the transition."""
    reason: OperationWorkflowUnmetInvariantReason

    def to_dict(self) -> dict[str, Any]:
        requirement = self.requirement.to_dict()

        reason: str = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "requirement": requirement,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_invariant_requirement import OperationWorkflowInvariantRequirement

        d = dict(src_dict)
        requirement = OperationWorkflowInvariantRequirement.from_dict(d.pop("requirement"))

        reason = check_operation_workflow_unmet_invariant_reason(d.pop("reason"))

        operation_workflow_unmet_invariant = cls(
            requirement=requirement,
            reason=reason,
        )

        return operation_workflow_unmet_invariant
