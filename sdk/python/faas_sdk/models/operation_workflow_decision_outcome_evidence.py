from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_decision import OperationBusinessDecision


T = TypeVar("T", bound="OperationWorkflowDecisionOutcomeEvidence")


@_attrs_define
class OperationWorkflowDecisionOutcomeEvidence:
    """Earliest retained report classifying this contributor for the selected decision outcome group. The optional business
    decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report
    itself. These are application-reported facts and do not establish causation.

    """

    report_id: UUID
    operation_id: UUID
    revision: int
    state: str
    occurred_at: datetime.datetime
    from_state: str | Unset = UNSET
    milestone_id: UUID | Unset = UNSET
    decision: OperationBusinessDecision | Unset = UNSET
    """Application-reported business decision with a public explanation and optional exact policy rule identity."""

    def to_dict(self) -> dict[str, Any]:
        report_id = str(self.report_id)

        operation_id = str(self.operation_id)

        revision = self.revision

        state = self.state

        occurred_at = self.occurred_at.isoformat()

        from_state = self.from_state

        milestone_id: str | Unset = UNSET
        if not isinstance(self.milestone_id, Unset):
            milestone_id = str(self.milestone_id)

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "report_id": report_id,
                "operation_id": operation_id,
                "revision": revision,
                "state": state,
                "occurred_at": occurred_at,
            }
        )
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if milestone_id is not UNSET:
            field_dict["milestone_id"] = milestone_id
        if decision is not UNSET:
            field_dict["decision"] = decision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_decision import OperationBusinessDecision

        d = dict(src_dict)
        report_id = UUID(d.pop("report_id"))

        operation_id = UUID(d.pop("operation_id"))

        revision = d.pop("revision")

        state = d.pop("state")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        from_state = d.pop("from_state", UNSET)

        _milestone_id = d.pop("milestone_id", UNSET)
        milestone_id: UUID | Unset
        if isinstance(_milestone_id, Unset):
            milestone_id = UNSET
        else:
            milestone_id = UUID(_milestone_id)

        _decision = d.pop("decision", UNSET)
        decision: OperationBusinessDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationBusinessDecision.from_dict(_decision)

        operation_workflow_decision_outcome_evidence = cls(
            report_id=report_id,
            operation_id=operation_id,
            revision=revision,
            state=state,
            occurred_at=occurred_at,
            from_state=from_state,
            milestone_id=milestone_id,
            decision=decision,
        )

        return operation_workflow_decision_outcome_evidence
