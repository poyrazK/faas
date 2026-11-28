from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
    from ..models.operation_workflow_obligation_transition_target import OperationWorkflowObligationTransitionTarget


T = TypeVar("T", bound="OperationWorkflowObligationTargetBinding")


@_attrs_define
class OperationWorkflowObligationTargetBinding:
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason. Unrelated edits
    preserve it; a new cycle never inherits it. Current readiness is independently reassessed.

    """

    obligation_id: str
    case: OperationWorkflowNextActionCase
    """Exact workflow case affected by a current source action."""
    transition: OperationWorkflowObligationTransitionTarget
    """Exact declared transition identity and pinned workflow contract version; it is an advisory target, not
    execution authorization."""
    reason: str
    bound_at: datetime.datetime
    bound_by: str

    def to_dict(self) -> dict[str, Any]:
        obligation_id = self.obligation_id

        case = self.case.to_dict()

        transition = self.transition.to_dict()

        reason = self.reason

        bound_at = self.bound_at.isoformat()

        bound_by = self.bound_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "obligation_id": obligation_id,
                "case": case,
                "transition": transition,
                "reason": reason,
                "bound_at": bound_at,
                "bound_by": bound_by,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
        from ..models.operation_workflow_obligation_transition_target import OperationWorkflowObligationTransitionTarget

        d = dict(src_dict)
        obligation_id = d.pop("obligation_id")

        case = OperationWorkflowNextActionCase.from_dict(d.pop("case"))

        transition = OperationWorkflowObligationTransitionTarget.from_dict(d.pop("transition"))

        reason = d.pop("reason")

        bound_at = datetime.datetime.fromisoformat(d.pop("bound_at"))

        bound_by = d.pop("bound_by")

        operation_workflow_obligation_target_binding = cls(
            obligation_id=obligation_id,
            case=case,
            transition=transition,
            reason=reason,
            bound_at=bound_at,
            bound_by=bound_by,
        )

        return operation_workflow_obligation_target_binding
