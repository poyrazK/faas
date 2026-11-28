from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_automatic_queue_entry_evidence_unavailable_reason import (
    WorkflowRecoveryAutomaticQueueEntryEvidenceUnavailableReason,
    check_workflow_recovery_automatic_queue_entry_evidence_unavailable_reason,
)
from ..models.workflow_recovery_automatic_queue_entry_wait_category import (
    WorkflowRecoveryAutomaticQueueEntryWaitCategory,
    check_workflow_recovery_automatic_queue_entry_wait_category,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_automatic_action import WorkflowRecoveryAutomaticAction
    from ..models.workflow_recovery_initial_queue_evidence import WorkflowRecoveryInitialQueueEvidence
    from ..models.workflow_recovery_queue_evidence import WorkflowRecoveryQueueEvidence


T = TypeVar("T", bound="WorkflowRecoveryAutomaticQueueEntry")


@_attrs_define
class WorkflowRecoveryAutomaticQueueEntry:
    """One pending or unresolved applied automatic retry. Queued_at is the frozen review preparation time. Ages are whole
    nonnegative seconds relative to the queue observed_at. Waiting age is omitted until the worker records a wait; the
    saved reason may be historical.

    """

    review_id: UUID
    policy_version: int
    queued_at: datetime.datetime
    queued_age_seconds: int
    action: WorkflowRecoveryAutomaticAction
    """Durable automatic retry intent and application receipt. Applied identifies child creation; its final result
    requires retained generation proof. Suppressed actions never run automatically."""
    initial_evidence: WorkflowRecoveryInitialQueueEvidence | Unset = UNSET
    """Read-only initial recovery evidence. Prepared identifies a planned generation with no submitted recovery job
    and is never repairable; submitted uses exact-generation proof from the initial evidence endpoint. Neither stage
    grants execution authority."""
    evidence: WorkflowRecoveryQueueEvidence | Unset = UNSET
    """Read-only exact-generation diagnosis for a returned retry queue entry; never a worker evaluation or
    admission grant. Parent repairability also requires the exact live generation to remain retained. Omitted for
    initial recoveries, which use their separate evidence endpoints."""
    evidence_unavailable_reason: WorkflowRecoveryAutomaticQueueEntryEvidenceUnavailableReason | Unset = UNSET
    wait_category: WorkflowRecoveryAutomaticQueueEntryWaitCategory | Unset = UNSET
    wait_reason: str | Unset = UNSET
    """Current pending policy pause/disable reason or the last saved worker reason; evidence diagnosis has its own
    observation time."""
    waiting_age_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        review_id = str(self.review_id)

        policy_version = self.policy_version

        queued_at = self.queued_at.isoformat()

        queued_age_seconds = self.queued_age_seconds

        action = self.action.to_dict()

        initial_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.initial_evidence, Unset):
            initial_evidence = self.initial_evidence.to_dict()

        evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = self.evidence.to_dict()

        evidence_unavailable_reason: str | Unset = UNSET
        if not isinstance(self.evidence_unavailable_reason, Unset):
            evidence_unavailable_reason = self.evidence_unavailable_reason

        wait_category: str | Unset = UNSET
        if not isinstance(self.wait_category, Unset):
            wait_category = self.wait_category

        wait_reason = self.wait_reason

        waiting_age_seconds = self.waiting_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "review_id": review_id,
                "policy_version": policy_version,
                "queued_at": queued_at,
                "queued_age_seconds": queued_age_seconds,
                "action": action,
            }
        )
        if initial_evidence is not UNSET:
            field_dict["initial_evidence"] = initial_evidence
        if evidence is not UNSET:
            field_dict["evidence"] = evidence
        if evidence_unavailable_reason is not UNSET:
            field_dict["evidence_unavailable_reason"] = evidence_unavailable_reason
        if wait_category is not UNSET:
            field_dict["wait_category"] = wait_category
        if wait_reason is not UNSET:
            field_dict["wait_reason"] = wait_reason
        if waiting_age_seconds is not UNSET:
            field_dict["waiting_age_seconds"] = waiting_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_automatic_action import WorkflowRecoveryAutomaticAction
        from ..models.workflow_recovery_initial_queue_evidence import WorkflowRecoveryInitialQueueEvidence
        from ..models.workflow_recovery_queue_evidence import WorkflowRecoveryQueueEvidence

        d = dict(src_dict)
        review_id = UUID(d.pop("review_id"))

        policy_version = d.pop("policy_version")

        queued_at = datetime.datetime.fromisoformat(d.pop("queued_at"))

        queued_age_seconds = d.pop("queued_age_seconds")

        action = WorkflowRecoveryAutomaticAction.from_dict(d.pop("action"))

        _initial_evidence = d.pop("initial_evidence", UNSET)
        initial_evidence: WorkflowRecoveryInitialQueueEvidence | Unset
        if isinstance(_initial_evidence, Unset):
            initial_evidence = UNSET
        else:
            initial_evidence = WorkflowRecoveryInitialQueueEvidence.from_dict(_initial_evidence)

        _evidence = d.pop("evidence", UNSET)
        evidence: WorkflowRecoveryQueueEvidence | Unset
        if isinstance(_evidence, Unset):
            evidence = UNSET
        else:
            evidence = WorkflowRecoveryQueueEvidence.from_dict(_evidence)

        _evidence_unavailable_reason = d.pop("evidence_unavailable_reason", UNSET)
        evidence_unavailable_reason: WorkflowRecoveryAutomaticQueueEntryEvidenceUnavailableReason | Unset
        if isinstance(_evidence_unavailable_reason, Unset):
            evidence_unavailable_reason = UNSET
        else:
            evidence_unavailable_reason = check_workflow_recovery_automatic_queue_entry_evidence_unavailable_reason(
                _evidence_unavailable_reason
            )

        _wait_category = d.pop("wait_category", UNSET)
        wait_category: WorkflowRecoveryAutomaticQueueEntryWaitCategory | Unset
        if isinstance(_wait_category, Unset):
            wait_category = UNSET
        else:
            wait_category = check_workflow_recovery_automatic_queue_entry_wait_category(_wait_category)

        wait_reason = d.pop("wait_reason", UNSET)

        waiting_age_seconds = d.pop("waiting_age_seconds", UNSET)

        workflow_recovery_automatic_queue_entry = cls(
            review_id=review_id,
            policy_version=policy_version,
            queued_at=queued_at,
            queued_age_seconds=queued_age_seconds,
            action=action,
            initial_evidence=initial_evidence,
            evidence=evidence,
            evidence_unavailable_reason=evidence_unavailable_reason,
            wait_category=wait_category,
            wait_reason=wait_reason,
            waiting_age_seconds=waiting_age_seconds,
        )

        workflow_recovery_automatic_queue_entry.additional_properties = d
        return workflow_recovery_automatic_queue_entry

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
