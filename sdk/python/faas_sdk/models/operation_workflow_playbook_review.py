from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_review_decision import (
    OperationWorkflowPlaybookReviewDecision,
    check_operation_workflow_playbook_review_decision,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_applicability import OperationWorkflowPlaybookApplicability


T = TypeVar("T", bound="OperationWorkflowPlaybookReview")


@_attrs_define
class OperationWorkflowPlaybookReview:
    """OperationWorkflowPlaybookReview. Durable human choice with server reviewer, observation context and immutable
    replacement reference. Not an application outcome or execution permission.

    """

    revision: int
    """OperationWorkflowPlaybookReview.revision. Expected current source revision for requests, committed new
    shared revision for retained decisions."""
    decision: OperationWorkflowPlaybookReviewDecision
    """OperationWorkflowPlaybookReview.decision. Human coordination choice; retire and replace permanently close
    this source checklist."""
    reason: str
    """OperationWorkflowPlaybookReview.reason. Required nonempty UTF-8 review reason, at most 1024 bytes and
    without control characters."""
    actor: str
    """OperationWorkflowPlaybookReview.actor. Authenticated account or API-key reviewer assigned by the server."""
    at: datetime.datetime
    """OperationWorkflowPlaybookReview.at. Server decision clock; application evidence was independently read."""
    applicability: OperationWorkflowPlaybookApplicability
    """OperationWorkflowPlaybookApplicability. Ephemeral applicability of a durable pinned checklist against
    current exact-case evidence. No progress, review history, readiness or outcome is mutated."""
    replacement_progress_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookReview.replacement_progress_id. Only for replace: distinct already-started active
    applicable checklist in the exact same case."""
    replacement_revision: int | Unset = UNSET
    """OperationWorkflowPlaybookReview.replacement_revision. Only for replace: expected target revision, rechecked
    with its active disposition under the writer lock."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        decision: str = self.decision

        reason = self.reason

        actor = self.actor

        at = self.at.isoformat()

        applicability = self.applicability.to_dict()

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
                "actor": actor,
                "at": at,
                "applicability": applicability,
            }
        )
        if replacement_progress_id is not UNSET:
            field_dict["replacement_progress_id"] = replacement_progress_id
        if replacement_revision is not UNSET:
            field_dict["replacement_revision"] = replacement_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_applicability import OperationWorkflowPlaybookApplicability

        d = dict(src_dict)
        revision = d.pop("revision")

        decision = check_operation_workflow_playbook_review_decision(d.pop("decision"))

        reason = d.pop("reason")

        actor = d.pop("actor")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        applicability = OperationWorkflowPlaybookApplicability.from_dict(d.pop("applicability"))

        _replacement_progress_id = d.pop("replacement_progress_id", UNSET)
        replacement_progress_id: UUID | Unset
        if isinstance(_replacement_progress_id, Unset):
            replacement_progress_id = UNSET
        else:
            replacement_progress_id = UUID(_replacement_progress_id)

        replacement_revision = d.pop("replacement_revision", UNSET)

        operation_workflow_playbook_review = cls(
            revision=revision,
            decision=decision,
            reason=reason,
            actor=actor,
            at=at,
            applicability=applicability,
            replacement_progress_id=replacement_progress_id,
            replacement_revision=replacement_revision,
        )

        return operation_workflow_playbook_review
