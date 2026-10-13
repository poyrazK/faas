from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_assessment_status import (
    OperationWorkflowObligationAssessmentStatus,
    check_operation_workflow_obligation_assessment_status,
)
from ..types import UNSET, Unset

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
    obligation_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        action_kind = self.action_kind

        status: str = self.status

        summary = self.summary

        checked_at = self.checked_at.isoformat()

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
        if obligation_id is not UNSET:
            field_dict["obligation_id"] = obligation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        action_kind = d.pop("action_kind")

        status = check_operation_workflow_obligation_assessment_status(d.pop("status"))

        summary = d.pop("summary")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        obligation_id = d.pop("obligation_id", UNSET)

        operation_workflow_obligation_assessment = cls(
            subscription_id=subscription_id,
            sequence=sequence,
            action_kind=action_kind,
            status=status,
            summary=summary,
            checked_at=checked_at,
            obligation_id=obligation_id,
        )

        return operation_workflow_obligation_assessment
