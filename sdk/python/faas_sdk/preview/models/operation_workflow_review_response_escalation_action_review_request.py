from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationActionReviewRequest")


@_attrs_define
class OperationWorkflowReviewResponseEscalationActionReviewRequest:
    """Explicit scoped review of the current completed coordinator action. Supply current episode timestamps and preserve
    parent owner, plan and exact check-in; omit parent reviews. Cannot combine with other response actions. Evidence
    unavailable is not clear or application success.

    """

    decision_at: datetime.datetime
    required_at: datetime.datetime
    handed_off_at: datetime.datetime
    escalated_at: datetime.datetime
    acknowledged_at: datetime.datetime
    completed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        handed_off_at = self.handed_off_at.isoformat()

        escalated_at = self.escalated_at.isoformat()

        acknowledged_at = self.acknowledged_at.isoformat()

        completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "handed_off_at": handed_off_at,
                "escalated_at": escalated_at,
                "acknowledged_at": acknowledged_at,
                "completed_at": completed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        handed_off_at = datetime.datetime.fromisoformat(d.pop("handed_off_at"))

        escalated_at = datetime.datetime.fromisoformat(d.pop("escalated_at"))

        acknowledged_at = datetime.datetime.fromisoformat(d.pop("acknowledged_at"))

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        operation_workflow_review_response_escalation_action_review_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            handed_off_at=handed_off_at,
            escalated_at=escalated_at,
            acknowledged_at=acknowledged_at,
            completed_at=completed_at,
        )

        return operation_workflow_review_response_escalation_action_review_request
