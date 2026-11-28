from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_evidence_check import OperationWorkflowPlaybookEvidenceCheck


T = TypeVar("T", bound="OperationWorkflowPlaybookEvidenceChange")


@_attrs_define
class OperationWorkflowPlaybookEvidenceChange:
    """OperationWorkflowPlaybookEvidenceChange. Opt-in durable checklist evidence monitoring; advisory observations
    preserve human reviews and application business authority.

    """

    before_status: str
    """OperationWorkflowPlaybookEvidenceChange.before_status. Pinned case identity or retained watch metadata."""
    before_reason: str
    """OperationWorkflowPlaybookEvidenceChange.before_reason. Pinned case identity or retained watch metadata."""
    after: OperationWorkflowPlaybookEvidenceCheck
    """OperationWorkflowPlaybookEvidenceCheck. Ephemeral case-read observation of a pinned requirement, separate
    from human review and attachment validity. Contract or guidance changes, missing declarations and incomplete
    coverage remain unavailable. Does not change progress or authorize execution."""

    def to_dict(self) -> dict[str, Any]:
        before_status = self.before_status

        before_reason = self.before_reason

        after = self.after.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "before_status": before_status,
                "before_reason": before_reason,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_evidence_check import OperationWorkflowPlaybookEvidenceCheck

        d = dict(src_dict)
        before_status = d.pop("before_status")

        before_reason = d.pop("before_reason")

        after = OperationWorkflowPlaybookEvidenceCheck.from_dict(d.pop("after"))

        operation_workflow_playbook_evidence_change = cls(
            before_status=before_status,
            before_reason=before_reason,
            after=after,
        )

        return operation_workflow_playbook_evidence_change
