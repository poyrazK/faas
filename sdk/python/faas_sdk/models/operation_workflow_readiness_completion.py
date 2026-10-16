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
    from ..models.operation_workflow_playbook_completion_outcome import OperationWorkflowPlaybookCompletionOutcome
    from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
    from ..models.operation_workflow_readiness_completion_evidence import OperationWorkflowReadinessCompletionEvidence
    from ..models.operation_workflow_readiness_outcome_escalation_policy import (
        OperationWorkflowReadinessOutcomeEscalationPolicy,
    )


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
    playbook_review_after_seconds: int | Unset = UNSET
    """OperationWorkflowReadinessCompletion.playbook_review_after_seconds. Optional review window after business-
    aware playbook completion; zero disables. Monitoring also ends with source inbox retention."""
    playbook_outcome: OperationWorkflowPlaybookCompletionOutcome | Unset = UNSET
    """OperationWorkflowPlaybookCompletionOutcome. Immutable human disposition and exact scoped advisory
    application evidence observed before completion; neither establishes causation nor authorizes execution."""
    escalation_policy: OperationWorkflowReadinessOutcomeEscalationPolicy | Unset = UNSET
    """Opt-in deadline escalation policy frozen at completion. Requires an outcome watch and future outcome
    deadline. Schedules one assigned follow-up on the deadline notification; never executes business actions."""
    watch_outcome: bool | Unset = UNSET
    """OperationWorkflowReadinessCompletion.watch_outcome. Opt in at completion to observe a pending or unknown
    business result; requires expected_outcome_code and a source workflow. The watch expires with source inbox
    retention."""
    outcome_deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCompletion.outcome_deadline_at. Optional future UTC deadline requiring
    watch_outcome. The first successful due check without an observed achieved outcome emits one notice; this is not
    business failure or a historical deadline verdict."""
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

        playbook_review_after_seconds = self.playbook_review_after_seconds

        playbook_outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_outcome, Unset):
            playbook_outcome = self.playbook_outcome.to_dict()

        escalation_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_policy, Unset):
            escalation_policy = self.escalation_policy.to_dict()

        watch_outcome = self.watch_outcome

        outcome_deadline_at: str | Unset = UNSET
        if not isinstance(self.outcome_deadline_at, Unset):
            outcome_deadline_at = self.outcome_deadline_at.isoformat()

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
        if playbook_review_after_seconds is not UNSET:
            field_dict["playbook_review_after_seconds"] = playbook_review_after_seconds
        if playbook_outcome is not UNSET:
            field_dict["playbook_outcome"] = playbook_outcome
        if escalation_policy is not UNSET:
            field_dict["escalation_policy"] = escalation_policy
        if watch_outcome is not UNSET:
            field_dict["watch_outcome"] = watch_outcome
        if outcome_deadline_at is not UNSET:
            field_dict["outcome_deadline_at"] = outcome_deadline_at
        if expected_outcome_code is not UNSET:
            field_dict["expected_outcome_code"] = expected_outcome_code
        if business_outcome is not UNSET:
            field_dict["business_outcome"] = business_outcome
        if watch_verification is not UNSET:
            field_dict["watch_verification"] = watch_verification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_completion_outcome import OperationWorkflowPlaybookCompletionOutcome
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
        from ..models.operation_workflow_readiness_completion_evidence import (
            OperationWorkflowReadinessCompletionEvidence,
        )
        from ..models.operation_workflow_readiness_outcome_escalation_policy import (
            OperationWorkflowReadinessOutcomeEscalationPolicy,
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

        playbook_review_after_seconds = d.pop("playbook_review_after_seconds", UNSET)

        _playbook_outcome = d.pop("playbook_outcome", UNSET)
        playbook_outcome: OperationWorkflowPlaybookCompletionOutcome | Unset
        if isinstance(_playbook_outcome, Unset):
            playbook_outcome = UNSET
        else:
            playbook_outcome = OperationWorkflowPlaybookCompletionOutcome.from_dict(_playbook_outcome)

        _escalation_policy = d.pop("escalation_policy", UNSET)
        escalation_policy: OperationWorkflowReadinessOutcomeEscalationPolicy | Unset
        if isinstance(_escalation_policy, Unset):
            escalation_policy = UNSET
        else:
            escalation_policy = OperationWorkflowReadinessOutcomeEscalationPolicy.from_dict(_escalation_policy)

        watch_outcome = d.pop("watch_outcome", UNSET)

        _outcome_deadline_at = d.pop("outcome_deadline_at", UNSET)
        outcome_deadline_at: datetime.datetime | Unset
        if isinstance(_outcome_deadline_at, Unset):
            outcome_deadline_at = UNSET
        else:
            outcome_deadline_at = datetime.datetime.fromisoformat(_outcome_deadline_at)

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
            playbook_review_after_seconds=playbook_review_after_seconds,
            playbook_outcome=playbook_outcome,
            escalation_policy=escalation_policy,
            watch_outcome=watch_outcome,
            outcome_deadline_at=outcome_deadline_at,
            expected_outcome_code=expected_outcome_code,
            business_outcome=business_outcome,
            watch_verification=watch_verification,
        )

        return operation_workflow_readiness_completion
