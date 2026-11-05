from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.job_task_attempt_response_status import (
    JobTaskAttemptResponseStatus,
    check_job_task_attempt_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.job_task_attempt_response_output_manifest import JobTaskAttemptResponseOutputManifest
    from ..models.work_decision import WorkDecision


T = TypeVar("T", bound="JobTaskAttemptResponse")


@_attrs_define
class JobTaskAttemptResponse:
    """Immutable terminal outcome of one task attempt."""

    run_id: UUID
    task_index: int
    attempt: int
    status: JobTaskAttemptResponseStatus
    finished_at: datetime.datetime
    log_content: str
    log_truncated: bool
    input_id: str | Unset = UNSET
    input_ref: str | Unset = UNSET
    work_decision: WorkDecision | Unset = UNSET
    """Persisted classifier decision for one execution result."""
    outcome_code: str | Unset = UNSET
    instance_id: UUID | Unset = UNSET
    error_class: str | Unset = UNSET
    error_message: str | Unset = UNSET
    exit_code: int | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    output_manifest: JobTaskAttemptResponseOutputManifest | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        task_index = self.task_index

        attempt = self.attempt

        status: str = self.status

        finished_at = self.finished_at.isoformat()

        log_content = self.log_content

        log_truncated = self.log_truncated

        input_id = self.input_id

        input_ref = self.input_ref

        work_decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.work_decision, Unset):
            work_decision = self.work_decision.to_dict()

        outcome_code = self.outcome_code

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        error_class = self.error_class

        error_message = self.error_message

        exit_code = self.exit_code

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        output_manifest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.output_manifest, Unset):
            output_manifest = self.output_manifest.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "task_index": task_index,
                "attempt": attempt,
                "status": status,
                "finished_at": finished_at,
                "log_content": log_content,
                "log_truncated": log_truncated,
            }
        )
        if input_id is not UNSET:
            field_dict["input_id"] = input_id
        if input_ref is not UNSET:
            field_dict["input_ref"] = input_ref
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
        if output_manifest is not UNSET:
            field_dict["output_manifest"] = output_manifest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_task_attempt_response_output_manifest import JobTaskAttemptResponseOutputManifest
        from ..models.work_decision import WorkDecision

        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        task_index = d.pop("task_index")

        attempt = d.pop("attempt")

        status = check_job_task_attempt_response_status(d.pop("status"))

        finished_at = datetime.datetime.fromisoformat(d.pop("finished_at"))

        log_content = d.pop("log_content")

        log_truncated = d.pop("log_truncated")

        input_id = d.pop("input_id", UNSET)

        input_ref = d.pop("input_ref", UNSET)

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

        error_class = d.pop("error_class", UNSET)

        error_message = d.pop("error_message", UNSET)

        exit_code = d.pop("exit_code", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _output_manifest = d.pop("output_manifest", UNSET)
        output_manifest: JobTaskAttemptResponseOutputManifest | Unset
        if isinstance(_output_manifest, Unset):
            output_manifest = UNSET
        else:
            output_manifest = JobTaskAttemptResponseOutputManifest.from_dict(_output_manifest)

        job_task_attempt_response = cls(
            run_id=run_id,
            task_index=task_index,
            attempt=attempt,
            status=status,
            finished_at=finished_at,
            log_content=log_content,
            log_truncated=log_truncated,
            input_id=input_id,
            input_ref=input_ref,
            work_decision=work_decision,
            outcome_code=outcome_code,
            instance_id=instance_id,
            error_class=error_class,
            error_message=error_message,
            exit_code=exit_code,
            started_at=started_at,
            output_manifest=output_manifest,
        )

        job_task_attempt_response.additional_properties = d
        return job_task_attempt_response

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
