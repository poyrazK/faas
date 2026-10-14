from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_progress_update_request_status import (
    OperationWorkflowPlaybookProgressUpdateRequestStatus,
    check_operation_workflow_playbook_progress_update_request_status,
)
from ..types import UNSET, Unset

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
    owner: str | Unset = UNSET
    """OperationWorkflowPlaybookProgressUpdateRequest.owner. Optional manual outcome-step reassignment; omission
    preserves the current owner and an empty value clears it. Requires outcome_code."""
    outcome_code: str | Unset = UNSET
    """OperationWorkflowPlaybookProgressUpdateRequest.outcome_code. Optional outcome-specific follow-up selector;
    item_index addresses that pinned step list. First review captures fresh comparable evidence of this exact
    outcome."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        item_index = self.item_index

        status: str = self.status

        note = self.note

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        owner = self.owner

        outcome_code = self.outcome_code

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
        if owner is not UNSET:
            field_dict["owner"] = owner
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code

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

        owner = d.pop("owner", UNSET)

        outcome_code = d.pop("outcome_code", UNSET)

        operation_workflow_playbook_progress_update_request = cls(
            revision=revision,
            item_index=item_index,
            status=status,
            note=note,
            evidence=evidence,
            owner=owner,
            outcome_code=outcome_code,
        )

        return operation_workflow_playbook_progress_update_request
