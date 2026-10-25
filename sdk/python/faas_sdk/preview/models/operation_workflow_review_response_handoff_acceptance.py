from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffAcceptance")


@_attrs_define
class OperationWorkflowReviewResponseHandoffAcceptance:
    """Immutable acceptance by an authenticated authorized operator for the responsible team, without verifying team
    membership or satisfying the original review deadline.

    """

    owner: str
    """OperationWorkflowReviewResponseHandoffAcceptance.owner. Responsible handed-off team at acceptance time."""
    actor: str
    """OperationWorkflowReviewResponseHandoffAcceptance.actor. Authenticated operator who recorded acceptance."""
    at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffAcceptance.at. Server acceptance time."""
    note: str
    """OperationWorkflowReviewResponseHandoffAcceptance.note. Operator acceptance note."""
    check_in_at: datetime.datetime
    """OperationWorkflowReviewResponseHandoffAcceptance.check_in_at. Future check-in committed at acceptance; later
    edits do not rewrite this record."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        actor = self.actor

        at = self.at.isoformat()

        note = self.note

        check_in_at = self.check_in_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "actor": actor,
                "at": at,
                "note": note,
                "check_in_at": check_in_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        owner = d.pop("owner")

        actor = d.pop("actor")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        note = d.pop("note")

        check_in_at = datetime.datetime.fromisoformat(d.pop("check_in_at"))

        operation_workflow_review_response_handoff_acceptance = cls(
            owner=owner,
            actor=actor,
            at=at,
            note=note,
            check_in_at=check_in_at,
        )

        return operation_workflow_review_response_handoff_acceptance
