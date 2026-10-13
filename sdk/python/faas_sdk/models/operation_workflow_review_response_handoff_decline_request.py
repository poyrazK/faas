from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffDeclineRequest")


@_attrs_define
class OperationWorkflowReviewResponseHandoffDeclineRequest:
    """Decline one current unaccepted handoff without a decision or acceptance. Retain its owner, plan and exact check-in,
    even if already overdue; deadlines do not restart.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffDeclineRequest.decision_at. Exact decision timestamp of the handed-off
    episode."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffDeclineRequest.required_at. Exact requirement timestamp; stale
    episodes conflict."""
    reason: str
    """OperationWorkflowReviewResponseHandoffDeclineRequest.reason. Nonblank UTF-8 reason up to 1024 bytes; line
    breaks and tabs allowed."""
    handed_off_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseHandoffDeclineRequest.handed_off_at. Current handoff timestamp; required by
    acceptance and decline after manual reassignment. Legacy automatic handoffs can omit it."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        reason = self.reason

        handed_off_at: str | Unset = UNSET
        if not isinstance(self.handed_off_at, Unset):
            handed_off_at = self.handed_off_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "reason": reason,
            }
        )
        if handed_off_at is not UNSET:
            field_dict["handed_off_at"] = handed_off_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        reason = d.pop("reason")

        _handed_off_at = d.pop("handed_off_at", UNSET)
        handed_off_at: datetime.datetime | Unset
        if isinstance(_handed_off_at, Unset):
            handed_off_at = UNSET
        else:
            handed_off_at = datetime.datetime.fromisoformat(_handed_off_at)

        operation_workflow_review_response_handoff_decline_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            reason=reason,
            handed_off_at=handed_off_at,
        )

        return operation_workflow_review_response_handoff_decline_request
