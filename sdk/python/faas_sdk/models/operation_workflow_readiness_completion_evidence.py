from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessCompletionEvidence")


@_attrs_define
class OperationWorkflowReadinessCompletionEvidence:
    """Server-observed advisory evidence before the revision-checked completion write. Workflow reports may change after
    this observation. Explicit boundaries never imply readiness.

    """

    checked_at: datetime.datetime
    """OperationWorkflowReadinessCompletionEvidence.checked_at. Retained follow-up completion value; never changes
    application evidence."""
    status: str
    """OperationWorkflowReadinessCompletionEvidence.status. Retained follow-up completion value; never changes
    application evidence."""
    explanation: str
    """OperationWorkflowReadinessCompletionEvidence.explanation. Retained follow-up completion value; never changes
    application evidence."""
    evidence_complete: bool
    """OperationWorkflowReadinessCompletionEvidence.evidence_complete. Retained follow-up completion value; never
    changes application evidence."""
    remaining_reasons: list[str]
    """OperationWorkflowReadinessCompletionEvidence.remaining_reasons. Retained follow-up completion value; never
    changes application evidence."""
    pending_verification_count: int
    """OperationWorkflowReadinessCompletionEvidence.pending_verification_count. Retained follow-up completion
    value; never changes application evidence."""
    warnings: list[str]
    """OperationWorkflowReadinessCompletionEvidence.warnings. Retained follow-up completion value; never changes
    application evidence."""
    reporting_operation_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessCompletionEvidence.reporting_operation_id. Retained follow-up completion value;
    never changes application evidence."""
    state_revision: int | Unset = UNSET
    """OperationWorkflowReadinessCompletionEvidence.state_revision. Retained follow-up completion value; never
    changes application evidence."""

    def to_dict(self) -> dict[str, Any]:
        checked_at = self.checked_at.isoformat()

        status = self.status

        explanation = self.explanation

        evidence_complete = self.evidence_complete

        remaining_reasons = self.remaining_reasons

        pending_verification_count = self.pending_verification_count

        warnings = self.warnings

        reporting_operation_id: str | Unset = UNSET
        if not isinstance(self.reporting_operation_id, Unset):
            reporting_operation_id = str(self.reporting_operation_id)

        state_revision = self.state_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "checked_at": checked_at,
                "status": status,
                "explanation": explanation,
                "evidence_complete": evidence_complete,
                "remaining_reasons": remaining_reasons,
                "pending_verification_count": pending_verification_count,
                "warnings": warnings,
            }
        )
        if reporting_operation_id is not UNSET:
            field_dict["reporting_operation_id"] = reporting_operation_id
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        status = d.pop("status")

        explanation = d.pop("explanation")

        evidence_complete = d.pop("evidence_complete")

        remaining_reasons = cast(list[str], d.pop("remaining_reasons"))

        pending_verification_count = d.pop("pending_verification_count")

        warnings = cast(list[str], d.pop("warnings"))

        _reporting_operation_id = d.pop("reporting_operation_id", UNSET)
        reporting_operation_id: UUID | Unset
        if isinstance(_reporting_operation_id, Unset):
            reporting_operation_id = UNSET
        else:
            reporting_operation_id = UUID(_reporting_operation_id)

        state_revision = d.pop("state_revision", UNSET)

        operation_workflow_readiness_completion_evidence = cls(
            checked_at=checked_at,
            status=status,
            explanation=explanation,
            evidence_complete=evidence_complete,
            remaining_reasons=remaining_reasons,
            pending_verification_count=pending_verification_count,
            warnings=warnings,
            reporting_operation_id=reporting_operation_id,
            state_revision=state_revision,
        )

        return operation_workflow_readiness_completion_evidence
