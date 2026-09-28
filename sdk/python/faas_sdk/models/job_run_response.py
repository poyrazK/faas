from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.job_run_response_aggregate_status import (
    JobRunResponseAggregateStatus,
    check_job_run_response_aggregate_status,
)
from ..models.job_run_response_execution_class import (
    JobRunResponseExecutionClass,
    check_job_run_response_execution_class,
)
from ..models.job_run_response_failure_policy import JobRunResponseFailurePolicy, check_job_run_response_failure_policy
from ..models.job_run_response_trigger_kind import JobRunResponseTriggerKind, check_job_run_response_trigger_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.job_run_response_effective_env_snapshot import JobRunResponseEffectiveEnvSnapshot
    from ..models.job_run_response_env_overrides import JobRunResponseEnvOverrides


T = TypeVar("T", bound="JobRunResponse")


@_attrs_define
class JobRunResponse:
    """Wire projection of state.JobRun. Aggregate counters are recomputed by schedd after every terminal task transition."""

    id: UUID
    job_id: UUID
    account_id: UUID
    trigger_kind: JobRunResponseTriggerKind
    tasks: int
    parallelism: int
    execution_class: JobRunResponseExecutionClass
    failure_policy: JobRunResponseFailurePolicy
    aggregate_status: JobRunResponseAggregateStatus
    tasks_succeeded: int
    tasks_failed: int
    tasks_cancelled: int
    tasks_running: int
    dead_letter_count: int
    created_at: datetime.datetime
    env_overrides: JobRunResponseEnvOverrides | Unset = UNSET
    input_manifest_version: int | Unset = UNSET
    """0 for numeric fan-out, 1 for an ordered inline or external input manifest."""
    input_digest: str | Unset = UNSET
    """SHA-256 of the canonical ordered input manifest."""
    input_manifest_uri: str | Unset = UNSET
    """Source obj:// URI for an external input manifest."""
    input_manifest_sha256: str | Unset = UNSET
    """SHA-256 of the external manifest's exact bytes."""
    eligible_at: datetime.datetime | Unset = UNSET
    latest_start_at: datetime.datetime | Unset = UNSET
    retry_max: int | Unset = UNSET
    task_timeout_sec: int | Unset = UNSET
    command: list[str] | Unset = UNSET
    """Command captured at run creation."""
    image_ref_snapshot: str | Unset = UNSET
    image_resolved_digest_snapshot: str | Unset = UNSET
    ram_mb_snapshot: int | Unset = UNSET
    effective_env_snapshot: JobRunResponseEffectiveEnvSnapshot | Unset = UNSET
    source_run_id: UUID | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        job_id = str(self.job_id)

        account_id = str(self.account_id)

        trigger_kind: str = self.trigger_kind

        tasks = self.tasks

        parallelism = self.parallelism

        execution_class: str = self.execution_class

        failure_policy: str = self.failure_policy

        aggregate_status: str = self.aggregate_status

        tasks_succeeded = self.tasks_succeeded

        tasks_failed = self.tasks_failed

        tasks_cancelled = self.tasks_cancelled

        tasks_running = self.tasks_running

        dead_letter_count = self.dead_letter_count

        created_at = self.created_at.isoformat()

        env_overrides: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_overrides, Unset):
            env_overrides = self.env_overrides.to_dict()

        input_manifest_version = self.input_manifest_version

        input_digest = self.input_digest

        input_manifest_uri = self.input_manifest_uri

        input_manifest_sha256 = self.input_manifest_sha256

        eligible_at: str | Unset = UNSET
        if not isinstance(self.eligible_at, Unset):
            eligible_at = self.eligible_at.isoformat()

        latest_start_at: str | Unset = UNSET
        if not isinstance(self.latest_start_at, Unset):
            latest_start_at = self.latest_start_at.isoformat()

        retry_max = self.retry_max

        task_timeout_sec = self.task_timeout_sec

        command: list[str] | Unset = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        image_ref_snapshot = self.image_ref_snapshot

        image_resolved_digest_snapshot = self.image_resolved_digest_snapshot

        ram_mb_snapshot = self.ram_mb_snapshot

        effective_env_snapshot: dict[str, Any] | Unset = UNSET
        if not isinstance(self.effective_env_snapshot, Unset):
            effective_env_snapshot = self.effective_env_snapshot.to_dict()

        source_run_id: str | Unset = UNSET
        if not isinstance(self.source_run_id, Unset):
            source_run_id = str(self.source_run_id)

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
                "job_id": job_id,
                "account_id": account_id,
                "trigger_kind": trigger_kind,
                "tasks": tasks,
                "parallelism": parallelism,
                "execution_class": execution_class,
                "failure_policy": failure_policy,
                "aggregate_status": aggregate_status,
                "tasks_succeeded": tasks_succeeded,
                "tasks_failed": tasks_failed,
                "tasks_cancelled": tasks_cancelled,
                "tasks_running": tasks_running,
                "dead_letter_count": dead_letter_count,
                "created_at": created_at,
            }
        )
        if env_overrides is not UNSET:
            field_dict["env_overrides"] = env_overrides
        if input_manifest_version is not UNSET:
            field_dict["input_manifest_version"] = input_manifest_version
        if input_digest is not UNSET:
            field_dict["input_digest"] = input_digest
        if input_manifest_uri is not UNSET:
            field_dict["input_manifest_uri"] = input_manifest_uri
        if input_manifest_sha256 is not UNSET:
            field_dict["input_manifest_sha256"] = input_manifest_sha256
        if eligible_at is not UNSET:
            field_dict["eligible_at"] = eligible_at
        if latest_start_at is not UNSET:
            field_dict["latest_start_at"] = latest_start_at
        if retry_max is not UNSET:
            field_dict["retry_max"] = retry_max
        if task_timeout_sec is not UNSET:
            field_dict["task_timeout_sec"] = task_timeout_sec
        if command is not UNSET:
            field_dict["command"] = command
        if image_ref_snapshot is not UNSET:
            field_dict["image_ref_snapshot"] = image_ref_snapshot
        if image_resolved_digest_snapshot is not UNSET:
            field_dict["image_resolved_digest_snapshot"] = image_resolved_digest_snapshot
        if ram_mb_snapshot is not UNSET:
            field_dict["ram_mb_snapshot"] = ram_mb_snapshot
        if effective_env_snapshot is not UNSET:
            field_dict["effective_env_snapshot"] = effective_env_snapshot
        if source_run_id is not UNSET:
            field_dict["source_run_id"] = source_run_id
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_run_response_effective_env_snapshot import JobRunResponseEffectiveEnvSnapshot
        from ..models.job_run_response_env_overrides import JobRunResponseEnvOverrides

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        job_id = UUID(d.pop("job_id"))

        account_id = UUID(d.pop("account_id"))

        trigger_kind = check_job_run_response_trigger_kind(d.pop("trigger_kind"))

        tasks = d.pop("tasks")

        parallelism = d.pop("parallelism")

        execution_class = check_job_run_response_execution_class(d.pop("execution_class"))

        failure_policy = check_job_run_response_failure_policy(d.pop("failure_policy"))

        aggregate_status = check_job_run_response_aggregate_status(d.pop("aggregate_status"))

        tasks_succeeded = d.pop("tasks_succeeded")

        tasks_failed = d.pop("tasks_failed")

        tasks_cancelled = d.pop("tasks_cancelled")

        tasks_running = d.pop("tasks_running")

        dead_letter_count = d.pop("dead_letter_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _env_overrides = d.pop("env_overrides", UNSET)
        env_overrides: JobRunResponseEnvOverrides | Unset
        if isinstance(_env_overrides, Unset):
            env_overrides = UNSET
        else:
            env_overrides = JobRunResponseEnvOverrides.from_dict(_env_overrides)

        input_manifest_version = d.pop("input_manifest_version", UNSET)

        input_digest = d.pop("input_digest", UNSET)

        input_manifest_uri = d.pop("input_manifest_uri", UNSET)

        input_manifest_sha256 = d.pop("input_manifest_sha256", UNSET)

        _eligible_at = d.pop("eligible_at", UNSET)
        eligible_at: datetime.datetime | Unset
        if isinstance(_eligible_at, Unset):
            eligible_at = UNSET
        else:
            eligible_at = datetime.datetime.fromisoformat(_eligible_at)

        _latest_start_at = d.pop("latest_start_at", UNSET)
        latest_start_at: datetime.datetime | Unset
        if isinstance(_latest_start_at, Unset):
            latest_start_at = UNSET
        else:
            latest_start_at = datetime.datetime.fromisoformat(_latest_start_at)

        retry_max = d.pop("retry_max", UNSET)

        task_timeout_sec = d.pop("task_timeout_sec", UNSET)

        command = cast(list[str], d.pop("command", UNSET))

        image_ref_snapshot = d.pop("image_ref_snapshot", UNSET)

        image_resolved_digest_snapshot = d.pop("image_resolved_digest_snapshot", UNSET)

        ram_mb_snapshot = d.pop("ram_mb_snapshot", UNSET)

        _effective_env_snapshot = d.pop("effective_env_snapshot", UNSET)
        effective_env_snapshot: JobRunResponseEffectiveEnvSnapshot | Unset
        if isinstance(_effective_env_snapshot, Unset):
            effective_env_snapshot = UNSET
        else:
            effective_env_snapshot = JobRunResponseEffectiveEnvSnapshot.from_dict(_effective_env_snapshot)

        _source_run_id = d.pop("source_run_id", UNSET)
        source_run_id: UUID | Unset
        if isinstance(_source_run_id, Unset):
            source_run_id = UNSET
        else:
            source_run_id = UUID(_source_run_id)

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

        job_run_response = cls(
            id=id,
            job_id=job_id,
            account_id=account_id,
            trigger_kind=trigger_kind,
            tasks=tasks,
            parallelism=parallelism,
            execution_class=execution_class,
            failure_policy=failure_policy,
            aggregate_status=aggregate_status,
            tasks_succeeded=tasks_succeeded,
            tasks_failed=tasks_failed,
            tasks_cancelled=tasks_cancelled,
            tasks_running=tasks_running,
            dead_letter_count=dead_letter_count,
            created_at=created_at,
            env_overrides=env_overrides,
            input_manifest_version=input_manifest_version,
            input_digest=input_digest,
            input_manifest_uri=input_manifest_uri,
            input_manifest_sha256=input_manifest_sha256,
            eligible_at=eligible_at,
            latest_start_at=latest_start_at,
            retry_max=retry_max,
            task_timeout_sec=task_timeout_sec,
            command=command,
            image_ref_snapshot=image_ref_snapshot,
            image_resolved_digest_snapshot=image_resolved_digest_snapshot,
            ram_mb_snapshot=ram_mb_snapshot,
            effective_env_snapshot=effective_env_snapshot,
            source_run_id=source_run_id,
            started_at=started_at,
            finished_at=finished_at,
        )

        job_run_response.additional_properties = d
        return job_run_response

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
