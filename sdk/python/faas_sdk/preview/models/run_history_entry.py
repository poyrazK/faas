from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.run_history_entry_result_validation_status import (
    RunHistoryEntryResultValidationStatus,
    check_run_history_entry_result_validation_status,
)
from ..models.run_history_entry_runtime import RunHistoryEntryRuntime, check_run_history_entry_runtime
from ..models.run_history_entry_status import RunHistoryEntryStatus, check_run_history_entry_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_task_ref import ExecutionTaskRef
    from ..models.execution_usage import ExecutionUsage
    from ..models.run_attribution import RunAttribution


T = TypeVar("T", bound="RunHistoryEntry")


@_attrs_define
class RunHistoryEntry:
    """One invocation's attribution, status and usage without its payload."""

    id: UUID
    status: RunHistoryEntryStatus
    runtime: RunHistoryEntryRuntime
    created_at: datetime.datetime
    elapsed_ms: int
    receipt_url: str
    result_validation_status: RunHistoryEntryResultValidationStatus | Unset = UNSET
    task: ExecutionTaskRef | Unset = UNSET
    """Identity of a published executable task version."""
    attribution: RunAttribution | Unset = UNSET
    """Verified customer and Agent references recorded with a Run."""
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    usage: ExecutionUsage | Unset = UNSET
    """Host-measured resource usage for a terminal execution."""
    failure_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        runtime: str = self.runtime

        created_at = self.created_at.isoformat()

        elapsed_ms = self.elapsed_ms

        receipt_url = self.receipt_url

        result_validation_status: str | Unset = UNSET
        if not isinstance(self.result_validation_status, Unset):
            result_validation_status = self.result_validation_status

        task: dict[str, Any] | Unset = UNSET
        if not isinstance(self.task, Unset):
            task = self.task.to_dict()

        attribution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attribution, Unset):
            attribution = self.attribution.to_dict()

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        usage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.usage, Unset):
            usage = self.usage.to_dict()

        failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "status": status,
                "runtime": runtime,
                "created_at": created_at,
                "elapsed_ms": elapsed_ms,
                "receipt_url": receipt_url,
            }
        )
        if result_validation_status is not UNSET:
            field_dict["result_validation_status"] = result_validation_status
        if task is not UNSET:
            field_dict["task"] = task
        if attribution is not UNSET:
            field_dict["attribution"] = attribution
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if usage is not UNSET:
            field_dict["usage"] = usage
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_task_ref import ExecutionTaskRef
        from ..models.execution_usage import ExecutionUsage
        from ..models.run_attribution import RunAttribution

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_run_history_entry_status(d.pop("status"))

        runtime = check_run_history_entry_runtime(d.pop("runtime"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        elapsed_ms = d.pop("elapsed_ms")

        receipt_url = d.pop("receipt_url")

        _result_validation_status = d.pop("result_validation_status", UNSET)
        result_validation_status: RunHistoryEntryResultValidationStatus | Unset
        if isinstance(_result_validation_status, Unset):
            result_validation_status = UNSET
        else:
            result_validation_status = check_run_history_entry_result_validation_status(_result_validation_status)

        _task = d.pop("task", UNSET)
        task: ExecutionTaskRef | Unset
        if isinstance(_task, Unset):
            task = UNSET
        else:
            task = ExecutionTaskRef.from_dict(_task)

        _attribution = d.pop("attribution", UNSET)
        attribution: RunAttribution | Unset
        if isinstance(_attribution, Unset):
            attribution = UNSET
        else:
            attribution = RunAttribution.from_dict(_attribution)

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

        _usage = d.pop("usage", UNSET)
        usage: ExecutionUsage | Unset
        if isinstance(_usage, Unset):
            usage = UNSET
        else:
            usage = ExecutionUsage.from_dict(_usage)

        failure_code = d.pop("failure_code", UNSET)

        run_history_entry = cls(
            id=id,
            status=status,
            runtime=runtime,
            created_at=created_at,
            elapsed_ms=elapsed_ms,
            receipt_url=receipt_url,
            result_validation_status=result_validation_status,
            task=task,
            attribution=attribution,
            started_at=started_at,
            finished_at=finished_at,
            usage=usage,
            failure_code=failure_code,
        )

        run_history_entry.additional_properties = d
        return run_history_entry

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
