from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestonePath")


@_attrs_define
class OperationWorkflowRetainedMilestonePath:
    """One uniquely named business path within an alternative group. Every declared retained fact must be verified together
    at the evaluation time; selectors are canonicalized and unique within the path.

    """

    id: str
    all_of: list[OperationWorkflowRetainedMilestoneRequirement]
    decision: OperationWorkflowPolicyRequirement | Unset = UNSET
    """Exact policy rule identity, version, and decision code required as fresh transition evidence."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        all_of = []
        for all_of_item_data in self.all_of:
            all_of_item = all_of_item_data.to_dict()
            all_of.append(all_of_item)

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "all_of": all_of,
            }
        )
        if decision is not UNSET:
            field_dict["decision"] = decision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        id = d.pop("id")

        all_of = []
        _all_of = d.pop("all_of")
        for all_of_item_data in _all_of:
            all_of_item = OperationWorkflowRetainedMilestoneRequirement.from_dict(all_of_item_data)

            all_of.append(all_of_item)

        _decision = d.pop("decision", UNSET)
        decision: OperationWorkflowPolicyRequirement | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationWorkflowPolicyRequirement.from_dict(_decision)

        operation_workflow_retained_milestone_path = cls(
            id=id,
            all_of=all_of,
            decision=decision,
        )

        return operation_workflow_retained_milestone_path
