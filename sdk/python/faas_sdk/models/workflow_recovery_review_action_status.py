from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_review_action_status_outcome import (
    WorkflowRecoveryReviewActionStatusOutcome,
    check_workflow_recovery_review_action_status_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryReviewActionStatus")


@_attrs_define
class WorkflowRecoveryReviewActionStatus:
    """Application freshness and execution outcome for one frozen action. A retry is complete only with saved terminal
    generation evidence or a terminal admission refusal.

    """

    position: int
    kind: str
    status: str
    reason: str
    outcome: WorkflowRecoveryReviewActionStatusOutcome
    complete: bool
    run_id: UUID
    resume_count: int
    """Exact execution generation; retry targets the next admitted generation."""
    run_url: str
    child_job_id: UUID | Unset = UNSET
    items_url: str | Unset = UNSET
    evidence_source: str | Unset = UNSET
    recorded_at: datetime.datetime | Unset = UNSET
    outcome_reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        position = self.position

        kind = self.kind

        status = self.status

        reason = self.reason

        outcome: str = self.outcome

        complete = self.complete

        run_id = str(self.run_id)

        resume_count = self.resume_count

        run_url = self.run_url

        child_job_id: str | Unset = UNSET
        if not isinstance(self.child_job_id, Unset):
            child_job_id = str(self.child_job_id)

        items_url = self.items_url

        evidence_source = self.evidence_source

        recorded_at: str | Unset = UNSET
        if not isinstance(self.recorded_at, Unset):
            recorded_at = self.recorded_at.isoformat()

        outcome_reason = self.outcome_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "position": position,
                "kind": kind,
                "status": status,
                "reason": reason,
                "outcome": outcome,
                "complete": complete,
                "run_id": run_id,
                "resume_count": resume_count,
                "run_url": run_url,
            }
        )
        if child_job_id is not UNSET:
            field_dict["child_job_id"] = child_job_id
        if items_url is not UNSET:
            field_dict["items_url"] = items_url
        if evidence_source is not UNSET:
            field_dict["evidence_source"] = evidence_source
        if recorded_at is not UNSET:
            field_dict["recorded_at"] = recorded_at
        if outcome_reason is not UNSET:
            field_dict["outcome_reason"] = outcome_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        position = d.pop("position")

        kind = d.pop("kind")

        status = d.pop("status")

        reason = d.pop("reason")

        outcome = check_workflow_recovery_review_action_status_outcome(d.pop("outcome"))

        complete = d.pop("complete")

        run_id = UUID(d.pop("run_id"))

        resume_count = d.pop("resume_count")

        run_url = d.pop("run_url")

        _child_job_id = d.pop("child_job_id", UNSET)
        child_job_id: UUID | Unset
        if isinstance(_child_job_id, Unset):
            child_job_id = UNSET
        else:
            child_job_id = UUID(_child_job_id)

        items_url = d.pop("items_url", UNSET)

        evidence_source = d.pop("evidence_source", UNSET)

        _recorded_at = d.pop("recorded_at", UNSET)
        recorded_at: datetime.datetime | Unset
        if isinstance(_recorded_at, Unset):
            recorded_at = UNSET
        else:
            recorded_at = datetime.datetime.fromisoformat(_recorded_at)

        outcome_reason = d.pop("outcome_reason", UNSET)

        workflow_recovery_review_action_status = cls(
            position=position,
            kind=kind,
            status=status,
            reason=reason,
            outcome=outcome,
            complete=complete,
            run_id=run_id,
            resume_count=resume_count,
            run_url=run_url,
            child_job_id=child_job_id,
            items_url=items_url,
            evidence_source=evidence_source,
            recorded_at=recorded_at,
            outcome_reason=outcome_reason,
        )

        workflow_recovery_review_action_status.additional_properties = d
        return workflow_recovery_review_action_status

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
