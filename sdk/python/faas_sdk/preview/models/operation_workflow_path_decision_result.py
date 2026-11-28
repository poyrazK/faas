from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_decision import OperationBusinessDecision
    from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement


T = TypeVar("T", bound="OperationWorkflowPathDecisionResult")


@_attrs_define
class OperationWorkflowPathDecisionResult:
    """Comparison of a pinned path decision requirement with planned application evidence. This is an observation and
    grants no execution authority.

    """

    requirement: OperationWorkflowPolicyRequirement
    """Exact policy rule identity, version, and decision code required as fresh transition evidence."""
    satisfied: bool
    reported: OperationBusinessDecision | Unset = UNSET
    """Application-reported business decision with a public explanation and optional exact policy rule identity."""

    def to_dict(self) -> dict[str, Any]:
        requirement = self.requirement.to_dict()

        satisfied = self.satisfied

        reported: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reported, Unset):
            reported = self.reported.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "requirement": requirement,
                "satisfied": satisfied,
            }
        )
        if reported is not UNSET:
            field_dict["reported"] = reported

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_decision import OperationBusinessDecision
        from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement

        d = dict(src_dict)
        requirement = OperationWorkflowPolicyRequirement.from_dict(d.pop("requirement"))

        satisfied = d.pop("satisfied")

        _reported = d.pop("reported", UNSET)
        reported: OperationBusinessDecision | Unset
        if isinstance(_reported, Unset):
            reported = UNSET
        else:
            reported = OperationBusinessDecision.from_dict(_reported)

        operation_workflow_path_decision_result = cls(
            requirement=requirement,
            satisfied=satisfied,
            reported=reported,
        )

        return operation_workflow_path_decision_result
