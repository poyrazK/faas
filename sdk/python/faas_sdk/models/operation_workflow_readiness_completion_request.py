from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_completion_request_outcome import (
    OperationWorkflowReadinessCompletionRequestOutcome,
    check_operation_workflow_readiness_completion_request_outcome,
)
from ..models.operation_workflow_readiness_completion_request_playbook_resolution import (
    OperationWorkflowReadinessCompletionRequestPlaybookResolution,
    check_operation_workflow_readiness_completion_request_playbook_resolution,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_outcome_escalation_policy import (
        OperationWorkflowReadinessOutcomeEscalationPolicy,
    )


T = TypeVar("T", bound="OperationWorkflowReadinessCompletionRequest")


@_attrs_define
class OperationWorkflowReadinessCompletionRequest:
    """An asserted coordination outcome. Generic completion requires a note and resolved requires retained evidence;
    playbook business completion captures its own current evidence. Closing does not prove business resolution.

    """

    outcome: OperationWorkflowReadinessCompletionRequestOutcome
    """OperationWorkflowReadinessCompletionRequest.outcome. Retained follow-up completion value; never changes
    application evidence."""
    note: str
    """OperationWorkflowReadinessCompletionRequest.note. Retained follow-up completion value; never changes
    application evidence."""
    evidence_operation_ids: list[UUID]
    """OperationWorkflowReadinessCompletionRequest.evidence_operation_ids. Retained follow-up completion value;
    never changes application evidence."""
    playbook_review_after_seconds: int | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.playbook_review_after_seconds. Optional review window after
    business-aware playbook completion; zero disables. Monitoring also ends with source inbox retention."""
    playbook_resolution: OperationWorkflowReadinessCompletionRequestPlaybookResolution | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.playbook_resolution. Optional business disposition for assigned
    playbook escalations. Expected or alternative resolutions require outcome resolved; canceled requires
    no_longer_applicable. Uses the frozen expectation, permits empty note and evidence IDs, and excludes extra
    outcome or verification watches."""
    escalation_policy: OperationWorkflowReadinessOutcomeEscalationPolicy | Unset = UNSET
    """Opt-in deadline escalation policy frozen at completion. Requires an outcome watch and future outcome
    deadline. Schedules one assigned follow-up on the deadline notification; never executes business actions."""
    watch_outcome: bool | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.watch_outcome. Opt in at completion to observe a pending or
    unknown business result; requires expected_outcome_code and a source workflow. The watch expires with source
    inbox retention."""
    outcome_deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.outcome_deadline_at. Optional future UTC deadline requiring
    watch_outcome. The first successful due check without an observed achieved outcome emits one notice; this is not
    business failure or a historical deadline verdict."""
    expected_outcome_code: str | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.expected_outcome_code. Optional caller-selected terminal outcome
    slug compared with application reports for the source workflow; never defines or modifies application success
    rules."""
    watch_verification: bool | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.watch_verification. Opt in at completion to watch up to 16
    currently pending saved resolutions; requires comparable current pending evidence."""

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        note = self.note

        evidence_operation_ids = []
        for evidence_operation_ids_item_data in self.evidence_operation_ids:
            evidence_operation_ids_item = str(evidence_operation_ids_item_data)
            evidence_operation_ids.append(evidence_operation_ids_item)

        playbook_review_after_seconds = self.playbook_review_after_seconds

        playbook_resolution: str | Unset = UNSET
        if not isinstance(self.playbook_resolution, Unset):
            playbook_resolution = self.playbook_resolution

        escalation_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_policy, Unset):
            escalation_policy = self.escalation_policy.to_dict()

        watch_outcome = self.watch_outcome

        outcome_deadline_at: str | Unset = UNSET
        if not isinstance(self.outcome_deadline_at, Unset):
            outcome_deadline_at = self.outcome_deadline_at.isoformat()

        expected_outcome_code = self.expected_outcome_code

        watch_verification = self.watch_verification

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome": outcome,
                "note": note,
                "evidence_operation_ids": evidence_operation_ids,
            }
        )
        if playbook_review_after_seconds is not UNSET:
            field_dict["playbook_review_after_seconds"] = playbook_review_after_seconds
        if playbook_resolution is not UNSET:
            field_dict["playbook_resolution"] = playbook_resolution
        if escalation_policy is not UNSET:
            field_dict["escalation_policy"] = escalation_policy
        if watch_outcome is not UNSET:
            field_dict["watch_outcome"] = watch_outcome
        if outcome_deadline_at is not UNSET:
            field_dict["outcome_deadline_at"] = outcome_deadline_at
        if expected_outcome_code is not UNSET:
            field_dict["expected_outcome_code"] = expected_outcome_code
        if watch_verification is not UNSET:
            field_dict["watch_verification"] = watch_verification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_outcome_escalation_policy import (
            OperationWorkflowReadinessOutcomeEscalationPolicy,
        )

        d = dict(src_dict)
        outcome = check_operation_workflow_readiness_completion_request_outcome(d.pop("outcome"))

        note = d.pop("note")

        evidence_operation_ids = []
        _evidence_operation_ids = d.pop("evidence_operation_ids")
        for evidence_operation_ids_item_data in _evidence_operation_ids:
            evidence_operation_ids_item = UUID(evidence_operation_ids_item_data)

            evidence_operation_ids.append(evidence_operation_ids_item)

        playbook_review_after_seconds = d.pop("playbook_review_after_seconds", UNSET)

        _playbook_resolution = d.pop("playbook_resolution", UNSET)
        playbook_resolution: OperationWorkflowReadinessCompletionRequestPlaybookResolution | Unset
        if isinstance(_playbook_resolution, Unset):
            playbook_resolution = UNSET
        else:
            playbook_resolution = check_operation_workflow_readiness_completion_request_playbook_resolution(
                _playbook_resolution
            )

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

        watch_verification = d.pop("watch_verification", UNSET)

        operation_workflow_readiness_completion_request = cls(
            outcome=outcome,
            note=note,
            evidence_operation_ids=evidence_operation_ids,
            playbook_review_after_seconds=playbook_review_after_seconds,
            playbook_resolution=playbook_resolution,
            escalation_policy=escalation_policy,
            watch_outcome=watch_outcome,
            outcome_deadline_at=outcome_deadline_at,
            expected_outcome_code=expected_outcome_code,
            watch_verification=watch_verification,
        )

        return operation_workflow_readiness_completion_request
