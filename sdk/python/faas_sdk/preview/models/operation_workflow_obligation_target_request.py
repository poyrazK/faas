from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
    from ..models.operation_workflow_obligation_transition_target import OperationWorkflowObligationTransitionTarget


T = TypeVar("T", bound="OperationWorkflowObligationTargetRequest")


@_attrs_define
class OperationWorkflowObligationTargetRequest:
    """Bind or clear one active free-form plan obligation for an exact case. Clear requires no transition; bind requires a
    fresh declared current-state transition. Requires the current coordination revision and unchanged status, owner,
    note and deadline; cannot combine with completion or review-response edits. Customer cases omit tenant IDs;
    operators specify the exact customer. Source obligation identity is rechecked under its write lock; evidence
    observation and persistence are independent.

    """

    obligation_id: str
    case: OperationWorkflowNextActionCase
    """Exact workflow case affected by a current source action."""
    reason: str
    transition: OperationWorkflowObligationTransitionTarget | Unset = UNSET
    """Exact declared transition identity and pinned workflow contract version; it is an advisory target, not
    execution authorization."""
    clear: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        obligation_id = self.obligation_id

        case = self.case.to_dict()

        reason = self.reason

        transition: dict[str, Any] | Unset = UNSET
        if not isinstance(self.transition, Unset):
            transition = self.transition.to_dict()

        clear = self.clear

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "obligation_id": obligation_id,
                "case": case,
                "reason": reason,
            }
        )
        if transition is not UNSET:
            field_dict["transition"] = transition
        if clear is not UNSET:
            field_dict["clear"] = clear

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
        from ..models.operation_workflow_obligation_transition_target import OperationWorkflowObligationTransitionTarget

        d = dict(src_dict)
        obligation_id = d.pop("obligation_id")

        case = OperationWorkflowNextActionCase.from_dict(d.pop("case"))

        reason = d.pop("reason")

        _transition = d.pop("transition", UNSET)
        transition: OperationWorkflowObligationTransitionTarget | Unset
        if isinstance(_transition, Unset):
            transition = UNSET
        else:
            transition = OperationWorkflowObligationTransitionTarget.from_dict(_transition)

        clear = d.pop("clear", UNSET)

        operation_workflow_obligation_target_request = cls(
            obligation_id=obligation_id,
            case=case,
            reason=reason,
            transition=transition,
            clear=clear,
        )

        return operation_workflow_obligation_target_request
