from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_review_request_decision import (
    OperationWorkflowPlaybookReviewRequestDecision,
    check_operation_workflow_playbook_review_request_decision,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybookReviewRequest")


@_attrs_define
class OperationWorkflowPlaybookReviewRequest:
    """OperationWorkflowPlaybookReviewRequest. Human review of frozen checklist coordination under a shared revision fence.
    No caller-supplied reviewer or applicability is accepted.

    """

    revision: int
    """OperationWorkflowPlaybookReviewRequest.revision. Expected current source revision for requests, committed
    new shared revision for retained decisions."""
    decision: OperationWorkflowPlaybookReviewRequestDecision
    """OperationWorkflowPlaybookReviewRequest.decision. Human coordination choice; retire and replace permanently
    close this source checklist."""
    reason: str
    """OperationWorkflowPlaybookReviewRequest.reason. Required nonempty UTF-8 review reason, at most 1024 bytes and
    without control characters."""
    replacement_progress_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookReviewRequest.replacement_progress_id. Only for replace: distinct already-started
    active applicable checklist in the exact same case."""
    replacement_revision: int | Unset = UNSET
    """OperationWorkflowPlaybookReviewRequest.replacement_revision. Only for replace: expected target revision,
    rechecked with its active disposition under the writer lock."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        decision: str = self.decision

        reason = self.reason

        replacement_progress_id: str | Unset = UNSET
        if not isinstance(self.replacement_progress_id, Unset):
            replacement_progress_id = str(self.replacement_progress_id)

        replacement_revision = self.replacement_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "decision": decision,
                "reason": reason,
            }
        )
        if replacement_progress_id is not UNSET:
            field_dict["replacement_progress_id"] = replacement_progress_id
        if replacement_revision is not UNSET:
            field_dict["replacement_revision"] = replacement_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        revision = d.pop("revision")

        decision = check_operation_workflow_playbook_review_request_decision(d.pop("decision"))

        reason = d.pop("reason")

        _replacement_progress_id = d.pop("replacement_progress_id", UNSET)
        replacement_progress_id: UUID | Unset
        if isinstance(_replacement_progress_id, Unset):
            replacement_progress_id = UNSET
        else:
            replacement_progress_id = UUID(_replacement_progress_id)

        replacement_revision = d.pop("replacement_revision", UNSET)

        operation_workflow_playbook_review_request = cls(
            revision=revision,
            decision=decision,
            reason=reason,
            replacement_progress_id=replacement_progress_id,
            replacement_revision=replacement_revision,
        )

        return operation_workflow_playbook_review_request
