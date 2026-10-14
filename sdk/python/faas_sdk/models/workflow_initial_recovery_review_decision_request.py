from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_initial_recovery_review_decision_request_decision import (
    WorkflowInitialRecoveryReviewDecisionRequestDecision,
    check_workflow_initial_recovery_review_decision_request_decision,
)

T = TypeVar("T", bound="WorkflowInitialRecoveryReviewDecisionRequest")


@_attrs_define
class WorkflowInitialRecoveryReviewDecisionRequest:
    """Decision and policy version for submitting an initial recovery review."""

    decision: WorkflowInitialRecoveryReviewDecisionRequestDecision
    reason: str

    def to_dict(self) -> dict[str, Any]:
        decision: str = self.decision

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision": decision,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision = check_workflow_initial_recovery_review_decision_request_decision(d.pop("decision"))

        reason = d.pop("reason")

        workflow_initial_recovery_review_decision_request = cls(
            decision=decision,
            reason=reason,
        )

        return workflow_initial_recovery_review_decision_request
