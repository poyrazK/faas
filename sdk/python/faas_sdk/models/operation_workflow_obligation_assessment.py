from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_assessment_status import (
    OperationWorkflowObligationAssessmentStatus,
    check_operation_workflow_obligation_assessment_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
    from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness


T = TypeVar("T", bound="OperationWorkflowObligationAssessment")


@_attrs_define
class OperationWorkflowObligationAssessment:
    """Coordination assessment for one displayed outstanding source action. Human reviews, ownership acceptance and worker
    checks are distinct from executing application transitions. Free-form response/coordinator plans remain
    fulfillment_unassessed without a declared binding. Deadlines and histories never create dependency guards or
    authorize actions.

    """

    subscription_id: UUID
    sequence: int
    action_kind: str
    status: OperationWorkflowObligationAssessmentStatus
    summary: str
    checked_at: datetime.datetime
    binding: OperationWorkflowObligationTargetBinding | Unset = UNSET
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    readiness: OperationWorkflowTransitionReadiness | Unset = UNSET
    """Ready means declared requirements match retained reports and the proposed milestone plan. It does not
    authorize or commit a transition or validate milestone payloads. All reported workflow prerequisites apply; only
    blockers targeting this Operation apply. Staleness, overdue deadlines, current-state reporting context and
    pending resolution proofs are advisories rather than undeclared guards. Reporting context uses current pinned
    expected_states, not invented required transition conditions. Planned reports do not replace this retained
    context."""
    obligation_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        action_kind = self.action_kind

        status: str = self.status

        summary = self.summary

        checked_at = self.checked_at.isoformat()

        binding: dict[str, Any] | Unset = UNSET
        if not isinstance(self.binding, Unset):
            binding = self.binding.to_dict()

        readiness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = self.readiness.to_dict()

        obligation_id = self.obligation_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "sequence": sequence,
                "action_kind": action_kind,
                "status": status,
                "summary": summary,
                "checked_at": checked_at,
            }
        )
        if binding is not UNSET:
            field_dict["binding"] = binding
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if obligation_id is not UNSET:
            field_dict["obligation_id"] = obligation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
        from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        action_kind = d.pop("action_kind")

        status = check_operation_workflow_obligation_assessment_status(d.pop("status"))

        summary = d.pop("summary")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        _binding = d.pop("binding", UNSET)
        binding: OperationWorkflowObligationTargetBinding | Unset
        if isinstance(_binding, Unset):
            binding = UNSET
        else:
            binding = OperationWorkflowObligationTargetBinding.from_dict(_binding)

        _readiness = d.pop("readiness", UNSET)
        readiness: OperationWorkflowTransitionReadiness | Unset
        if isinstance(_readiness, Unset):
            readiness = UNSET
        else:
            readiness = OperationWorkflowTransitionReadiness.from_dict(_readiness)

        obligation_id = d.pop("obligation_id", UNSET)

        operation_workflow_obligation_assessment = cls(
            subscription_id=subscription_id,
            sequence=sequence,
            action_kind=action_kind,
            status=status,
            summary=summary,
            checked_at=checked_at,
            binding=binding,
            readiness=readiness,
            obligation_id=obligation_id,
        )

        return operation_workflow_obligation_assessment
