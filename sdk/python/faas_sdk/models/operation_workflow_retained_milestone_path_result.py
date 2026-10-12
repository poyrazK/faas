from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_path_decision_result import OperationWorkflowPathDecisionResult
    from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone


T = TypeVar("T", bound="OperationWorkflowRetainedMilestonePathResult")


@_attrs_define
class OperationWorkflowRetainedMilestonePathResult:
    """Complete evidence findings for one named alternative path. Satisfied means every requirement is verified at the
    shared evaluation time; passing facts from separate paths cannot combine to satisfy a group.

    """

    id: str
    satisfied: bool
    requirements: list[OperationWorkflowRetainedMilestone]
    decision: OperationWorkflowPathDecisionResult | Unset = UNSET
    """Comparison of a pinned path decision requirement with planned application evidence. This is an observation
    and grants no execution authority."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        satisfied = self.satisfied

        requirements = []
        for requirements_item_data in self.requirements:
            requirements_item = requirements_item_data.to_dict()
            requirements.append(requirements_item)

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "satisfied": satisfied,
                "requirements": requirements,
            }
        )
        if decision is not UNSET:
            field_dict["decision"] = decision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_path_decision_result import OperationWorkflowPathDecisionResult
        from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone

        d = dict(src_dict)
        id = d.pop("id")

        satisfied = d.pop("satisfied")

        requirements = []
        _requirements = d.pop("requirements")
        for requirements_item_data in _requirements:
            requirements_item = OperationWorkflowRetainedMilestone.from_dict(requirements_item_data)

            requirements.append(requirements_item)

        _decision = d.pop("decision", UNSET)
        decision: OperationWorkflowPathDecisionResult | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationWorkflowPathDecisionResult.from_dict(_decision)

        operation_workflow_retained_milestone_path_result = cls(
            id=id,
            satisfied=satisfied,
            requirements=requirements,
            decision=decision,
        )

        return operation_workflow_retained_milestone_path_result
