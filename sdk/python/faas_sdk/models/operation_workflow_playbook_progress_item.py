from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_progress_item_status import (
    OperationWorkflowPlaybookProgressItemStatus,
    check_operation_workflow_playbook_progress_item_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_evidence import OperationWorkflowPlaybookEvidence
    from ..models.operation_workflow_playbook_evidence_check import OperationWorkflowPlaybookEvidenceCheck
    from ..models.operation_workflow_playbook_step_readiness import OperationWorkflowPlaybookStepReadiness


T = TypeVar("T", bound="OperationWorkflowPlaybookProgressItem")


@_attrs_define
class OperationWorkflowPlaybookProgressItem:
    """OperationWorkflowPlaybookProgressItem. Durable advisory checklist coordination, independent of application business
    success.

    """

    label: str
    """OperationWorkflowPlaybookProgressItem.label. Frozen application-authored evidence requirement."""
    status: OperationWorkflowPlaybookProgressItemStatus
    """OperationWorkflowPlaybookProgressItem.status. Human checklist review state."""
    note: str
    """OperationWorkflowPlaybookProgressItem.note. Latest reviewer note."""
    evidence: list[OperationWorkflowPlaybookEvidence]
    """OperationWorkflowPlaybookProgressItem.evidence. Scoped references checked when edited; future retention
    availability is not guaranteed."""
    readiness: OperationWorkflowPlaybookStepReadiness | Unset = UNSET
    """OperationWorkflowPlaybookStepReadiness. Human review dependency status, not business state or execution
    authority."""
    owner: str | Unset = UNSET
    """OperationWorkflowPlaybookProgressItem.owner. Outcome follow-up team label; human reassignment is recorded
    with the step review."""
    due_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowPlaybookProgressItem.due_at. Pinned completion deadline from first list review, independent
    of business success."""
    evidence_check: OperationWorkflowPlaybookEvidenceCheck | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceCheck. Ephemeral case-read observation of a pinned requirement, separate
    from human review and attachment validity. Contract or guidance changes, missing declarations and incomplete
    coverage remain unavailable. Does not change progress or authorize execution."""
    reviewed_by: str | Unset = UNSET
    """OperationWorkflowPlaybookProgressItem.reviewed_by. Authenticated account or API-key reviewer, assigned by
    the server."""
    reviewed_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowPlaybookProgressItem.reviewed_at. Server review clock for the latest edit."""

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        status: str = self.status

        note = self.note

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        readiness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = self.readiness.to_dict()

        owner = self.owner

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        evidence_check: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence_check, Unset):
            evidence_check = self.evidence_check.to_dict()

        reviewed_by = self.reviewed_by

        reviewed_at: str | Unset = UNSET
        if not isinstance(self.reviewed_at, Unset):
            reviewed_at = self.reviewed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "status": status,
                "note": note,
                "evidence": evidence,
            }
        )
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if owner is not UNSET:
            field_dict["owner"] = owner
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if evidence_check is not UNSET:
            field_dict["evidence_check"] = evidence_check
        if reviewed_by is not UNSET:
            field_dict["reviewed_by"] = reviewed_by
        if reviewed_at is not UNSET:
            field_dict["reviewed_at"] = reviewed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_evidence import OperationWorkflowPlaybookEvidence
        from ..models.operation_workflow_playbook_evidence_check import OperationWorkflowPlaybookEvidenceCheck
        from ..models.operation_workflow_playbook_step_readiness import OperationWorkflowPlaybookStepReadiness

        d = dict(src_dict)
        label = d.pop("label")

        status = check_operation_workflow_playbook_progress_item_status(d.pop("status"))

        note = d.pop("note")

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = OperationWorkflowPlaybookEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        _readiness = d.pop("readiness", UNSET)
        readiness: OperationWorkflowPlaybookStepReadiness | Unset
        if isinstance(_readiness, Unset):
            readiness = UNSET
        else:
            readiness = OperationWorkflowPlaybookStepReadiness.from_dict(_readiness)

        owner = d.pop("owner", UNSET)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        _evidence_check = d.pop("evidence_check", UNSET)
        evidence_check: OperationWorkflowPlaybookEvidenceCheck | Unset
        if isinstance(_evidence_check, Unset):
            evidence_check = UNSET
        else:
            evidence_check = OperationWorkflowPlaybookEvidenceCheck.from_dict(_evidence_check)

        reviewed_by = d.pop("reviewed_by", UNSET)

        _reviewed_at = d.pop("reviewed_at", UNSET)
        reviewed_at: datetime.datetime | Unset
        if isinstance(_reviewed_at, Unset):
            reviewed_at = UNSET
        else:
            reviewed_at = datetime.datetime.fromisoformat(_reviewed_at)

        operation_workflow_playbook_progress_item = cls(
            label=label,
            status=status,
            note=note,
            evidence=evidence,
            readiness=readiness,
            owner=owner,
            due_at=due_at,
            evidence_check=evidence_check,
            reviewed_by=reviewed_by,
            reviewed_at=reviewed_at,
        )

        return operation_workflow_playbook_progress_item
