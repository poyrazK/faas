from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_execution import EventRecoveryExecution
    from ..models.workflow_initial_evidence_repair_receipt import WorkflowInitialEvidenceRepairReceipt


T = TypeVar("T", bound="WorkflowInitialEvidenceDiagnosis")


@_attrs_define
class WorkflowInitialEvidenceDiagnosis:
    """Consistent diagnosis of retained evidence for an initial recovery review."""

    review_id: UUID
    run_id: UUID
    resume_count: int
    observed_at: datetime.datetime
    reason: str
    repairable: bool
    job_id: UUID | Unset = UNSET
    execution: EventRecoveryExecution | Unset = UNSET
    """Read-only observation of the exact admitted replay or workflow resume. Invocation mode prefers saved
    confirmed terminal results, retained with the recovery job. Workflow mode prefers saved terminal results for the
    admitted resume, then live retained run and resume evidence. Missing evidence without a saved result is unknown.
    Native failed/dead runs both report failed. Pending and running workflow states report queued and running;
    awaiting_event also reports running. Without a saved result, later native resumes report superseded with no
    completion time. Saved native results survive workflow-history pruning for the recovery job retention period;
    the first confirmed terminal result wins across later resumes. Omitted for routing recovery and items not
    admitted."""
    repair: WorkflowInitialEvidenceRepairReceipt | Unset = UNSET
    """Recorded result of an initial recovery evidence repair."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        review_id = str(self.review_id)

        run_id = str(self.run_id)

        resume_count = self.resume_count

        observed_at = self.observed_at.isoformat()

        reason = self.reason

        repairable = self.repairable

        job_id: str | Unset = UNSET
        if not isinstance(self.job_id, Unset):
            job_id = str(self.job_id)

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        repair: dict[str, Any] | Unset = UNSET
        if not isinstance(self.repair, Unset):
            repair = self.repair.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "review_id": review_id,
                "run_id": run_id,
                "resume_count": resume_count,
                "observed_at": observed_at,
                "reason": reason,
                "repairable": repairable,
            }
        )
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if execution is not UNSET:
            field_dict["execution"] = execution
        if repair is not UNSET:
            field_dict["repair"] = repair

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_execution import EventRecoveryExecution
        from ..models.workflow_initial_evidence_repair_receipt import WorkflowInitialEvidenceRepairReceipt

        d = dict(src_dict)
        review_id = UUID(d.pop("review_id"))

        run_id = UUID(d.pop("run_id"))

        resume_count = d.pop("resume_count")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        reason = d.pop("reason")

        repairable = d.pop("repairable")

        _job_id = d.pop("job_id", UNSET)
        job_id: UUID | Unset
        if isinstance(_job_id, Unset):
            job_id = UNSET
        else:
            job_id = UUID(_job_id)

        _execution = d.pop("execution", UNSET)
        execution: EventRecoveryExecution | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = EventRecoveryExecution.from_dict(_execution)

        _repair = d.pop("repair", UNSET)
        repair: WorkflowInitialEvidenceRepairReceipt | Unset
        if isinstance(_repair, Unset):
            repair = UNSET
        else:
            repair = WorkflowInitialEvidenceRepairReceipt.from_dict(_repair)

        workflow_initial_evidence_diagnosis = cls(
            review_id=review_id,
            run_id=run_id,
            resume_count=resume_count,
            observed_at=observed_at,
            reason=reason,
            repairable=repairable,
            job_id=job_id,
            execution=execution,
            repair=repair,
        )

        workflow_initial_evidence_diagnosis.additional_properties = d
        return workflow_initial_evidence_diagnosis

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
