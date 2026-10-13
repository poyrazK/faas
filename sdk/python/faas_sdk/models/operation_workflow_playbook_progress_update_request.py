from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_progress_update_request_status import (
    OperationWorkflowPlaybookProgressUpdateRequestStatus,
    check_operation_workflow_playbook_progress_update_request_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_evidence import OperationWorkflowPlaybookEvidence


T = TypeVar("T", bound="OperationWorkflowPlaybookProgressUpdateRequest")


@_attrs_define
class OperationWorkflowPlaybookProgressUpdateRequest:
    """OperationWorkflowPlaybookProgressUpdateRequest. Durable advisory checklist coordination, independent of application
    business success.

    """

    revision: int
    """OperationWorkflowPlaybookProgressUpdateRequest.revision. Expected current progress revision; conflicting
    edits return 409."""
    item_index: int
    """OperationWorkflowPlaybookProgressUpdateRequest.item_index. Zero-based index in the frozen evidence
    checklist."""
    status: OperationWorkflowPlaybookProgressUpdateRequestStatus
    """OperationWorkflowPlaybookProgressUpdateRequest.status. Human review state; completed requires at least one
    scoped evidence reference."""
    note: str
    """OperationWorkflowPlaybookProgressUpdateRequest.note. Required review note, trimmed nonempty and without
    control characters."""
    evidence: list[OperationWorkflowPlaybookEvidence]
    """OperationWorkflowPlaybookProgressUpdateRequest.evidence. Replacement evidence references for this item;
    empty allowed only for pending."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        item_index = self.item_index

        status: str = self.status

        note = self.note

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "item_index": item_index,
                "status": status,
                "note": note,
                "evidence": evidence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_evidence import OperationWorkflowPlaybookEvidence

        d = dict(src_dict)
        revision = d.pop("revision")

        item_index = d.pop("item_index")

        status = check_operation_workflow_playbook_progress_update_request_status(d.pop("status"))

        note = d.pop("note")

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = OperationWorkflowPlaybookEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        operation_workflow_playbook_progress_update_request = cls(
            revision=revision,
            item_index=item_index,
            status=status,
            note=note,
            evidence=evidence,
        )

        return operation_workflow_playbook_progress_update_request
