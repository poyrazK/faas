from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.job_task_response_error_class import JobTaskResponseErrorClass, check_job_task_response_error_class
from ..models.job_task_response_status import JobTaskResponseStatus, check_job_task_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.job_task_response_output_manifest import JobTaskResponseOutputManifest
    from ..models.work_decision import WorkDecision


T = TypeVar("T", bound="JobTaskResponse")


@_attrs_define
class JobTaskResponse:
    """Wire projection of state.JobTask. LeaseToken is intentionally omitted (internal dispatch primitive)."""

    run_id: UUID
    task_index: int
    status: JobTaskResponseStatus
    attempt: int
    created_at: datetime.datetime
    input_id: str | Unset = UNSET
    """Stable declared input identity for this task."""
    input_ref: str | Unset = UNSET
    """Opaque input reference passed to the guest."""
    source_task_index: int | Unset = UNSET
    """Original task index when this task came from replay-failed."""
    output_manifest: JobTaskResponseOutputManifest | Unset = UNSET
    """Versioned artifact references published by a successful task. Gregale retains metadata, not artifact bytes."""
    work_decision: WorkDecision | Unset = UNSET
    """Persisted classifier decision for one execution result."""
    outcome_code: str | Unset = UNSET
    instance_id: UUID | Unset = UNSET
    error_class: JobTaskResponseErrorClass | Unset = UNSET
    error_message: str | Unset = UNSET
    exit_code: int | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        task_index = self.task_index

        status: str = self.status

        attempt = self.attempt

        created_at = self.created_at.isoformat()

        input_id = self.input_id

        input_ref = self.input_ref

        source_task_index = self.source_task_index

        output_manifest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.output_manifest, Unset):
            output_manifest = self.output_manifest.to_dict()

        work_decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.work_decision, Unset):
            work_decision = self.work_decision.to_dict()

        outcome_code = self.outcome_code

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        error_class: str | Unset = UNSET
        if not isinstance(self.error_class, Unset):
            error_class = self.error_class

        error_message = self.error_message

        exit_code = self.exit_code

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
                "run_id": run_id,
                "task_index": task_index,
                "status": status,
                "attempt": attempt,
                "created_at": created_at,
            }
        )
        if input_id is not UNSET:
            field_dict["input_id"] = input_id
        if input_ref is not UNSET:
            field_dict["input_ref"] = input_ref
        if source_task_index is not UNSET:
            field_dict["source_task_index"] = source_task_index
        if output_manifest is not UNSET:
            field_dict["output_manifest"] = output_manifest
        if work_decision is not UNSET:
            field_dict["work_decision"] = work_decision
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if error_class is not UNSET:
            field_dict["error_class"] = error_class
        if error_message is not UNSET:
            field_dict["error_message"] = error_message
        if exit_code is not UNSET:
            field_dict["exit_code"] = exit_code
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_task_response_output_manifest import JobTaskResponseOutputManifest
        from ..models.work_decision import WorkDecision

        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        task_index = d.pop("task_index")

        status = check_job_task_response_status(d.pop("status"))

        attempt = d.pop("attempt")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        input_id = d.pop("input_id", UNSET)

        input_ref = d.pop("input_ref", UNSET)

        source_task_index = d.pop("source_task_index", UNSET)

        _output_manifest = d.pop("output_manifest", UNSET)
        output_manifest: JobTaskResponseOutputManifest | Unset
        if isinstance(_output_manifest, Unset):
            output_manifest = UNSET
        else:
            output_manifest = JobTaskResponseOutputManifest.from_dict(_output_manifest)

        _work_decision = d.pop("work_decision", UNSET)
        work_decision: WorkDecision | Unset
        if isinstance(_work_decision, Unset):
            work_decision = UNSET
        else:
            work_decision = WorkDecision.from_dict(_work_decision)

        outcome_code = d.pop("outcome_code", UNSET)

        _instance_id = d.pop("instance_id", UNSET)
        instance_id: UUID | Unset
        if isinstance(_instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = UUID(_instance_id)

        _error_class = d.pop("error_class", UNSET)
        error_class: JobTaskResponseErrorClass | Unset
        if isinstance(_error_class, Unset):
            error_class = UNSET
        else:
            error_class = check_job_task_response_error_class(_error_class)

        error_message = d.pop("error_message", UNSET)

        exit_code = d.pop("exit_code", UNSET)

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

        job_task_response = cls(
            run_id=run_id,
            task_index=task_index,
            status=status,
            attempt=attempt,
            created_at=created_at,
            input_id=input_id,
            input_ref=input_ref,
            source_task_index=source_task_index,
            output_manifest=output_manifest,
            work_decision=work_decision,
            outcome_code=outcome_code,
            instance_id=instance_id,
            error_class=error_class,
            error_message=error_message,
            exit_code=exit_code,
            started_at=started_at,
            finished_at=finished_at,
        )

        job_task_response.additional_properties = d
        return job_task_response

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
