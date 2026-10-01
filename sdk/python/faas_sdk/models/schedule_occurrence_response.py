from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.schedule_occurrence_response_status import (
    ScheduleOccurrenceResponseStatus,
    check_schedule_occurrence_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.schedule_policy import SchedulePolicy
    from ..models.work_decision import WorkDecision


T = TypeVar("T", bound="ScheduleOccurrenceResponse")


@_attrs_define
class ScheduleOccurrenceResponse:
    """Durable decision and lifecycle snapshot for one nominal schedule time."""

    id: UUID
    schedule_revision: int
    scheduled_for: datetime.datetime
    schedule_policy: SchedulePolicy
    """Versioned recurring-work scheduling policy for Jobs and both HTTP and command Crons. HTTP replace waits for
    a prior dispatched request to complete because the scheduler has no stop acknowledgement for a request already
    delivered to the app."""
    status: ScheduleOccurrenceResponseStatus
    created_at: datetime.datetime
    start_deadline_at: datetime.datetime | Unset = UNSET
    reason: str | Unset = UNSET
    work_decision: WorkDecision | Unset = UNSET
    """Persisted classifier decision for one execution result."""
    outcome_code: str | Unset = UNSET
    blocking_occurrence_id: UUID | Unset = UNSET
    job_run_id: UUID | Unset = UNSET
    invocation_id: UUID | Unset = UNSET
    app_task_id: UUID | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        schedule_revision = self.schedule_revision

        scheduled_for = self.scheduled_for.isoformat()

        schedule_policy = self.schedule_policy.to_dict()

        status: str = self.status

        created_at = self.created_at.isoformat()

        start_deadline_at: str | Unset = UNSET
        if not isinstance(self.start_deadline_at, Unset):
            start_deadline_at = self.start_deadline_at.isoformat()

        reason = self.reason

        work_decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.work_decision, Unset):
            work_decision = self.work_decision.to_dict()

        outcome_code = self.outcome_code

        blocking_occurrence_id: str | Unset = UNSET
        if not isinstance(self.blocking_occurrence_id, Unset):
            blocking_occurrence_id = str(self.blocking_occurrence_id)

        job_run_id: str | Unset = UNSET
        if not isinstance(self.job_run_id, Unset):
            job_run_id = str(self.job_run_id)

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        app_task_id: str | Unset = UNSET
        if not isinstance(self.app_task_id, Unset):
            app_task_id = str(self.app_task_id)

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "schedule_revision": schedule_revision,
                "scheduled_for": scheduled_for,
                "schedule_policy": schedule_policy,
                "status": status,
                "created_at": created_at,
            }
        )
        if start_deadline_at is not UNSET:
            field_dict["start_deadline_at"] = start_deadline_at
        if reason is not UNSET:
            field_dict["reason"] = reason
        if work_decision is not UNSET:
            field_dict["work_decision"] = work_decision
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code
        if blocking_occurrence_id is not UNSET:
            field_dict["blocking_occurrence_id"] = blocking_occurrence_id
        if job_run_id is not UNSET:
            field_dict["job_run_id"] = job_run_id
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if app_task_id is not UNSET:
            field_dict["app_task_id"] = app_task_id
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.schedule_policy import SchedulePolicy
        from ..models.work_decision import WorkDecision

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        schedule_revision = d.pop("schedule_revision")

        scheduled_for = datetime.datetime.fromisoformat(d.pop("scheduled_for"))

        schedule_policy = SchedulePolicy.from_dict(d.pop("schedule_policy"))

        status = check_schedule_occurrence_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _start_deadline_at = d.pop("start_deadline_at", UNSET)
        start_deadline_at: datetime.datetime | Unset
        if isinstance(_start_deadline_at, Unset):
            start_deadline_at = UNSET
        else:
            start_deadline_at = datetime.datetime.fromisoformat(_start_deadline_at)

        reason = d.pop("reason", UNSET)

        _work_decision = d.pop("work_decision", UNSET)
        work_decision: WorkDecision | Unset
        if isinstance(_work_decision, Unset):
            work_decision = UNSET
        else:
            work_decision = WorkDecision.from_dict(_work_decision)

        outcome_code = d.pop("outcome_code", UNSET)

        _blocking_occurrence_id = d.pop("blocking_occurrence_id", UNSET)
        blocking_occurrence_id: UUID | Unset
        if isinstance(_blocking_occurrence_id, Unset):
            blocking_occurrence_id = UNSET
        else:
            blocking_occurrence_id = UUID(_blocking_occurrence_id)

        _job_run_id = d.pop("job_run_id", UNSET)
        job_run_id: UUID | Unset
        if isinstance(_job_run_id, Unset):
            job_run_id = UNSET
        else:
            job_run_id = UUID(_job_run_id)

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        _app_task_id = d.pop("app_task_id", UNSET)
        app_task_id: UUID | Unset
        if isinstance(_app_task_id, Unset):
            app_task_id = UNSET
        else:
            app_task_id = UUID(_app_task_id)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        schedule_occurrence_response = cls(
            id=id,
            schedule_revision=schedule_revision,
            scheduled_for=scheduled_for,
            schedule_policy=schedule_policy,
            status=status,
            created_at=created_at,
            start_deadline_at=start_deadline_at,
            reason=reason,
            work_decision=work_decision,
            outcome_code=outcome_code,
            blocking_occurrence_id=blocking_occurrence_id,
            job_run_id=job_run_id,
            invocation_id=invocation_id,
            app_task_id=app_task_id,
            started_at=started_at,
            finished_at=finished_at,
        )

        schedule_occurrence_response.additional_properties = d
        return schedule_occurrence_response

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
