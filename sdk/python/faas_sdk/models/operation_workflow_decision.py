from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_decision_reason import (
    OperationWorkflowDecisionReason,
    check_operation_workflow_decision_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_blocker import OperationWorkflowBlocker
    from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition


T = TypeVar("T", bound="OperationWorkflowDecision")


@_attrs_define
class OperationWorkflowDecision:
    """Observational explanation from the selected contract and latest reported state, independent of fact pagination.
    These options are not execution authorization. Required milestones must be committed with the next transition;
    retained historical facts do not satisfy them.

    """

    reason: OperationWorkflowDecisionReason
    explanation: str
    needs_attention: bool
    """True when state is unknown, stale, overdue, has application-reported blockers, or has unresolved direct
    dependencies."""
    next_actions: list[OperationWorkflowInstanceTransition]
    blockers: list[OperationWorkflowBlocker] | Unset = UNSET
    state_revision: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        explanation = self.explanation

        needs_attention = self.needs_attention

        next_actions = []
        for next_actions_item_data in self.next_actions:
            next_actions_item = next_actions_item_data.to_dict()
            next_actions.append(next_actions_item)

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        state_revision = self.state_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reason": reason,
                "explanation": explanation,
                "needs_attention": needs_attention,
                "next_actions": next_actions,
            }
        )
        if blockers is not UNSET:
            field_dict["blockers"] = blockers
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_blocker import OperationWorkflowBlocker
        from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition

        d = dict(src_dict)
        reason = check_operation_workflow_decision_reason(d.pop("reason"))

        explanation = d.pop("explanation")

        needs_attention = d.pop("needs_attention")

        next_actions = []
        _next_actions = d.pop("next_actions")
        for next_actions_item_data in _next_actions:
            next_actions_item = OperationWorkflowInstanceTransition.from_dict(next_actions_item_data)

            next_actions.append(next_actions_item)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[OperationWorkflowBlocker] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = OperationWorkflowBlocker.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        state_revision = d.pop("state_revision", UNSET)

        operation_workflow_decision = cls(
            reason=reason,
            explanation=explanation,
            needs_attention=needs_attention,
            next_actions=next_actions,
            blockers=blockers,
            state_revision=state_revision,
        )

        return operation_workflow_decision
