from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffReassignmentRequest")


@_attrs_define
class OperationWorkflowReviewResponseHandoffReassignmentRequest:
    """Reassign a currently declined handoff to a different parent response owner. Preserve plan, check-in and review
    deadline; start a new acceptance clock. Cannot combine with a decision, acceptance or decline.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffReassignmentRequest.decision_at. Exact reviewed decision timestamp."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffReassignmentRequest.required_at. Exact review requirement timestamp."""
    declined_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffReassignmentRequest.declined_at. Exact decline timestamp to route;
    stale decline conflicts."""
    reason: str
    """OperationWorkflowReviewResponseHandoffReassignmentRequest.reason. Nonblank UTF-8 explanation up to 1024
    bytes; line breaks and tabs allowed."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        declined_at = self.declined_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "declined_at": declined_at,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        declined_at = datetime.datetime.fromisoformat(d.pop("declined_at"))

        reason = d.pop("reason")

        operation_workflow_review_response_handoff_reassignment_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            declined_at=declined_at,
            reason=reason,
        )

        return operation_workflow_review_response_handoff_reassignment_request
