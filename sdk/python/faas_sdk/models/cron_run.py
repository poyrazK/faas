from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.cron_run_outcome import CronRunOutcome, check_cron_run_outcome
from ..types import UNSET, Unset

T = TypeVar("T", bound="CronRun")


@_attrs_define
class CronRun:
    """One execution of a cron: when it fired, how long it ran, and how it ended. HTTP fires project invocations; command
    fires project deployment-attached app tasks.

    """

    id: str
    """Invocation id for HTTP crons or task UUID for command crons."""
    started_at: datetime.datetime
    """When the cron fired (the invocation/task creation time), not when the app began executing."""
    outcome: CronRunOutcome
    """Normalized result. `timeout` means the dispatch exceeded its deadline; `dead_letter` means the retry budget
    was exhausted; `running` means the run has not reached a terminal state yet. Branch on this, never on `error`.
   """
    attempts: int
    """Dispatch attempts for this run; greater than 1 means it was retried."""
    completed_at: datetime.datetime | None | Unset = UNSET
    """When the run reached a terminal state; null while still in flight."""
    duration_ms: int | None | Unset = UNSET
    """completed_at - started_at in milliseconds, computed server-side. Null while the run is still in flight."""
    instance_id: None | str | Unset = UNSET
    """The instance that served the run; null if the fire never reached one."""
    task_id: UUID | Unset = UNSET
    """Deployment-attached app-task receipt for command crons."""
    error: None | str | Unset = UNSET
    """Operator-facing failure text. Unstructured and unversioned — do not parse it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        started_at = self.started_at.isoformat()

        outcome: str = self.outcome

        attempts = self.attempts

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        duration_ms: int | None | Unset
        if isinstance(self.duration_ms, Unset):
            duration_ms = UNSET
        else:
            duration_ms = self.duration_ms

        instance_id: None | str | Unset
        if isinstance(self.instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = self.instance_id

        task_id: str | Unset = UNSET
        if not isinstance(self.task_id, Unset):
            task_id = str(self.task_id)

        error: None | str | Unset
        if isinstance(self.error, Unset):
            error = UNSET
        else:
            error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "started_at": started_at,
                "outcome": outcome,
                "attempts": attempts,
            }
        )
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if duration_ms is not UNSET:
            field_dict["duration_ms"] = duration_ms
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if task_id is not UNSET:
            field_dict["task_id"] = task_id
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        outcome = check_cron_run_outcome(d.pop("outcome"))

        attempts = d.pop("attempts")

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        def _parse_duration_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        duration_ms = _parse_duration_ms(d.pop("duration_ms", UNSET))

        def _parse_instance_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        instance_id = _parse_instance_id(d.pop("instance_id", UNSET))

        _task_id = d.pop("task_id", UNSET)
        task_id: UUID | Unset
        if isinstance(_task_id, Unset):
            task_id = UNSET
        else:
            task_id = UUID(_task_id)

        def _parse_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error = _parse_error(d.pop("error", UNSET))

        cron_run = cls(
            id=id,
            started_at=started_at,
            outcome=outcome,
            attempts=attempts,
            completed_at=completed_at,
            duration_ms=duration_ms,
            instance_id=instance_id,
            task_id=task_id,
            error=error,
        )

        cron_run.additional_properties = d
        return cron_run

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
