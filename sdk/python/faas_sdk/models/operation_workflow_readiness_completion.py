from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_completion_outcome import (
    OperationWorkflowReadinessCompletionOutcome,
    check_operation_workflow_readiness_completion_outcome,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
    from ..models.operation_workflow_readiness_completion_evidence import OperationWorkflowReadinessCompletionEvidence


T = TypeVar("T", bound="OperationWorkflowReadinessCompletion")


@_attrs_define
class OperationWorkflowReadinessCompletion:
    """Immutable recorded follow-up completion with server-derived actor, write time and advisory pre-write evidence check.
    Retained with the original notification.

    """

    outcome: OperationWorkflowReadinessCompletionOutcome
    """OperationWorkflowReadinessCompletion.outcome. Retained follow-up completion value; never changes application
    evidence."""
    note: str
    """OperationWorkflowReadinessCompletion.note. Retained follow-up completion value; never changes application
    evidence."""
    evidence_operation_ids: list[UUID]
    """OperationWorkflowReadinessCompletion.evidence_operation_ids. Retained follow-up completion value; never
    changes application evidence."""
    completed_at: datetime.datetime
    """OperationWorkflowReadinessCompletion.completed_at. Retained follow-up completion value; never changes
    application evidence."""
    completed_by: str
    """OperationWorkflowReadinessCompletion.completed_by. Retained follow-up completion value; never changes
    application evidence."""
    evidence: OperationWorkflowReadinessCompletionEvidence
    """Server-observed advisory evidence before the revision-checked completion write. Workflow reports may change
    after this observation. Explicit boundaries never imply readiness."""
    expected_outcome_code: str | Unset = UNSET
    """OperationWorkflowReadinessCompletion.expected_outcome_code. Optional caller-selected terminal outcome slug
    compared with application reports for the source workflow; never defines or modifies application success rules.
   """
    business_outcome: OperationWorkflowReadinessBusinessOutcome | Unset = UNSET
    """Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing
    state, stale reports, contract changes, older revisions and terminal reports without outcome codes remain
    unknown. Explicit outcome codes do not imply causation or authorize execution. Completion snapshots are
    immutable; current checks are live reads."""
    watch_verification: bool | Unset = UNSET
    """OperationWorkflowReadinessCompletion.watch_verification. Opt in at completion to watch up to 16 currently
    pending saved resolutions; requires comparable current pending evidence."""

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        note = self.note

        evidence_operation_ids = []
        for evidence_operation_ids_item_data in self.evidence_operation_ids:
            evidence_operation_ids_item = str(evidence_operation_ids_item_data)
            evidence_operation_ids.append(evidence_operation_ids_item)

        completed_at = self.completed_at.isoformat()

        completed_by = self.completed_by

        evidence = self.evidence.to_dict()

        expected_outcome_code = self.expected_outcome_code

        business_outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_outcome, Unset):
            business_outcome = self.business_outcome.to_dict()

        watch_verification = self.watch_verification

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome": outcome,
                "note": note,
                "evidence_operation_ids": evidence_operation_ids,
                "completed_at": completed_at,
                "completed_by": completed_by,
                "evidence": evidence,
            }
        )
        if expected_outcome_code is not UNSET:
            field_dict["expected_outcome_code"] = expected_outcome_code
        if business_outcome is not UNSET:
            field_dict["business_outcome"] = business_outcome
        if watch_verification is not UNSET:
            field_dict["watch_verification"] = watch_verification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
        from ..models.operation_workflow_readiness_completion_evidence import (
            OperationWorkflowReadinessCompletionEvidence,
        )

        d = dict(src_dict)
        outcome = check_operation_workflow_readiness_completion_outcome(d.pop("outcome"))

        note = d.pop("note")

        evidence_operation_ids = []
        _evidence_operation_ids = d.pop("evidence_operation_ids")
        for evidence_operation_ids_item_data in _evidence_operation_ids:
            evidence_operation_ids_item = UUID(evidence_operation_ids_item_data)

            evidence_operation_ids.append(evidence_operation_ids_item)

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        completed_by = d.pop("completed_by")

        evidence = OperationWorkflowReadinessCompletionEvidence.from_dict(d.pop("evidence"))

        expected_outcome_code = d.pop("expected_outcome_code", UNSET)

        _business_outcome = d.pop("business_outcome", UNSET)
        business_outcome: OperationWorkflowReadinessBusinessOutcome | Unset
        if isinstance(_business_outcome, Unset):
            business_outcome = UNSET
        else:
            business_outcome = OperationWorkflowReadinessBusinessOutcome.from_dict(_business_outcome)

        watch_verification = d.pop("watch_verification", UNSET)

        operation_workflow_readiness_completion = cls(
            outcome=outcome,
            note=note,
            evidence_operation_ids=evidence_operation_ids,
            completed_at=completed_at,
            completed_by=completed_by,
            evidence=evidence,
            expected_outcome_code=expected_outcome_code,
            business_outcome=business_outcome,
            watch_verification=watch_verification,
        )

        return operation_workflow_readiness_completion
