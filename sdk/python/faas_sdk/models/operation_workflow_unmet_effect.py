from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_unmet_effect_reason import (
    OperationWorkflowUnmetEffectReason,
    check_operation_workflow_unmet_effect_reason,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_effect_requirement import OperationWorkflowEffectRequirement


T = TypeVar("T", bound="OperationWorkflowUnmetEffect")


@_attrs_define
class OperationWorkflowUnmetEffect:
    """A required effect confirmation that is absent or incompatible with the planned evidence."""

    requirement: OperationWorkflowEffectRequirement
    """Exact effect code and version that must be confirmed by fresh transition evidence."""
    reason: OperationWorkflowUnmetEffectReason

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
        from ..models.operation_workflow_effect_requirement import OperationWorkflowEffectRequirement

        d = dict(src_dict)
        requirement = OperationWorkflowEffectRequirement.from_dict(d.pop("requirement"))

        reason = check_operation_workflow_unmet_effect_reason(d.pop("reason"))

        operation_workflow_unmet_effect = cls(
            requirement=requirement,
            reason=reason,
        )

        return operation_workflow_unmet_effect
