from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_revision_evidence_kind import (
    OperationWorkflowRevisionEvidenceKind,
    check_operation_workflow_revision_evidence_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowRevisionEvidence")


@_attrs_define
class OperationWorkflowRevisionEvidence:
    """Links to reported decision or milestone facts and explicitly unverified expected verification facts. These
    references do not establish a cause for the revision.

    """

    kind: OperationWorkflowRevisionEvidenceKind
    operation_id: UUID
    milestone_id: UUID
    milestone: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        operation_id = str(self.operation_id)

        milestone_id = str(self.milestone_id)

        milestone = self.milestone

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "operation_id": operation_id,
                "milestone_id": milestone_id,
            }
        )
        if milestone is not UNSET:
            field_dict["milestone"] = milestone

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_operation_workflow_revision_evidence_kind(d.pop("kind"))

        operation_id = UUID(d.pop("operation_id"))

        milestone_id = UUID(d.pop("milestone_id"))

        milestone = d.pop("milestone", UNSET)

        operation_workflow_revision_evidence = cls(
            kind=kind,
            operation_id=operation_id,
            milestone_id=milestone_id,
            milestone=milestone,
        )

        return operation_workflow_revision_evidence
