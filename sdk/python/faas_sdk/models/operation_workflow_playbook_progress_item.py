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
        if reviewed_by is not UNSET:
            field_dict["reviewed_by"] = reviewed_by
        if reviewed_at is not UNSET:
            field_dict["reviewed_at"] = reviewed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_evidence import OperationWorkflowPlaybookEvidence

        d = dict(src_dict)
        label = d.pop("label")

        status = check_operation_workflow_playbook_progress_item_status(d.pop("status"))

        note = d.pop("note")

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = OperationWorkflowPlaybookEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

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
            reviewed_by=reviewed_by,
            reviewed_at=reviewed_at,
        )

        return operation_workflow_playbook_progress_item
