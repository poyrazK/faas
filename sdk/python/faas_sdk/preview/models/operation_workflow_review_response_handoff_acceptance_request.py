from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffAcceptanceRequest")


@_attrs_define
class OperationWorkflowReviewResponseHandoffAcceptanceRequest:
    """Accept the current reassigned review for its responsible team. Parent response must remain active with the handed-
    off owner and a future check-in, without an accompanying decision.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffAcceptanceRequest.decision_at. Exact decision timestamp of the handed-
    off episode."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffAcceptanceRequest.required_at. Exact requirement timestamp of the
    handed-off episode; stale episodes conflict."""
    note: str
    """OperationWorkflowReviewResponseHandoffAcceptanceRequest.note. Nonblank UTF-8 acceptance note up to 1024
    bytes; line breaks and tabs allowed."""
    handed_off_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseHandoffAcceptanceRequest.handed_off_at. Current handoff timestamp; required
    by acceptance and decline after manual reassignment. Legacy automatic handoffs can omit it."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        note = self.note

        handed_off_at: str | Unset = UNSET
        if not isinstance(self.handed_off_at, Unset):
            handed_off_at = self.handed_off_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "note": note,
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

        note = d.pop("note")

        _handed_off_at = d.pop("handed_off_at", UNSET)
        handed_off_at: datetime.datetime | Unset
        if isinstance(_handed_off_at, Unset):
            handed_off_at = UNSET
        else:
            handed_off_at = datetime.datetime.fromisoformat(_handed_off_at)

        operation_workflow_review_response_handoff_acceptance_request = cls(
            decision_at=decision_at,
            required_at=required_at,
            note=note,
            handed_off_at=handed_off_at,
        )

        return operation_workflow_review_response_handoff_acceptance_request
