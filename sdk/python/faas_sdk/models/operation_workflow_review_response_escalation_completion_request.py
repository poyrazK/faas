from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationCompletionRequest")


@_attrs_define
class OperationWorkflowReviewResponseEscalationCompletionRequest:
    """Complete one current escalation coordinator commitment with an action summary and saved review evidence. Preserve
    parent owner, plan and exact check-in, even if overdue; omit parent reviews. Cannot combine with acknowledgment,
    decision, acceptance, decline or reassignment. Accepted or declined handoffs can still record completed work for the
    same retained episode, but replaced or resolved episodes cannot. Does not establish application success.

    """

    decision_at: datetime.datetime
    required_at: datetime.datetime
    handed_off_at: datetime.datetime
    escalated_at: datetime.datetime
    acknowledged_at: datetime.datetime
    summary: str
    """Nonblank UTF-8 summary of performed coordination work, limited to 1024 bytes; line breaks and tabs are
    allowed."""
    review_indices: list[int]
    """Zero-based indices into the current parent response reviews. References are copied into immutable evidence;
    they are not fresh application verification or a claim that blockers cleared."""
    review_after_seconds: int | Unset = UNSET
    """Optional one-shot evidence check delay from server completion time, 60 seconds to seven days. Omission
    leaves the completion unscheduled."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        handed_off_at = self.handed_off_at.isoformat()

        escalated_at = self.escalated_at.isoformat()

        acknowledged_at = self.acknowledged_at.isoformat()

        summary = self.summary

        review_indices = self.review_indices

        review_after_seconds = self.review_after_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "handed_off_at": handed_off_at,
                "escalated_at": escalated_at,
                "acknowledged_at": acknowledged_at,
                "summary": summary,
                "review_indices": review_indices,
            }
        )
        if review_after_seconds is not UNSET:
            field_dict["review_after_seconds"] = review_after_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        handed_off_at = datetime.datetime.fromisoformat(d.pop("handed_off_at"))

        escalated_at = datetime.datetime.fromisoformat(d.pop("escalated_at"))

        acknowledged_at = datetime.datetime.fromisoformat(d.pop("acknowledged_at"))

        summary = d.pop("summary")

        review_indices = cast(list[int], d.pop("review_indices"))

        review_after_seconds = d.pop("review_after_seconds", UNSET)

        operation_workflow_review_response_escalation_completion_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            handed_off_at=handed_off_at,
            escalated_at=escalated_at,
            acknowledged_at=acknowledged_at,
            summary=summary,
            review_indices=review_indices,
            review_after_seconds=review_after_seconds,
        )

        return operation_workflow_review_response_escalation_completion_request
