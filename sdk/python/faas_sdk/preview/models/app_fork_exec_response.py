from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_fork_exec_response_status import AppForkExecResponseStatus, check_app_fork_exec_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_fork_exec_response_failure import AppForkExecResponseFailure


T = TypeVar("T", bound="AppForkExecResponse")


@_attrs_define
class AppForkExecResponse:
    """One command run inside a production fork (ADR-732) and, once finished, its result."""

    id: UUID
    fork_id: UUID
    command: list[str]
    timeout_seconds: int
    max_output_bytes: int
    status: AppForkExecResponseStatus
    output_truncated: bool
    stdout: str
    """Tail of standard output."""
    stderr: str
    """Tail of standard error."""
    requested_by: str
    created_at: datetime.datetime
    shell: bool | Unset = UNSET
    exit_code: int | Unset = UNSET
    failure: AppForkExecResponseFailure | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        fork_id = str(self.fork_id)

        command = self.command

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        status: str = self.status

        output_truncated = self.output_truncated

        stdout = self.stdout

        stderr = self.stderr

        requested_by = self.requested_by

        created_at = self.created_at.isoformat()

        shell = self.shell

        exit_code = self.exit_code

        failure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure, Unset):
            failure = self.failure.to_dict()

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
                "fork_id": fork_id,
                "command": command,
                "timeout_seconds": timeout_seconds,
                "max_output_bytes": max_output_bytes,
                "status": status,
                "output_truncated": output_truncated,
                "stdout": stdout,
                "stderr": stderr,
                "requested_by": requested_by,
                "created_at": created_at,
            }
        )
        if shell is not UNSET:
            field_dict["shell"] = shell
        if exit_code is not UNSET:
            field_dict["exit_code"] = exit_code
        if failure is not UNSET:
            field_dict["failure"] = failure
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_fork_exec_response_failure import AppForkExecResponseFailure

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        fork_id = UUID(d.pop("fork_id"))

        command = cast(list[str], d.pop("command"))

        timeout_seconds = d.pop("timeout_seconds")

        max_output_bytes = d.pop("max_output_bytes")

        status = check_app_fork_exec_response_status(d.pop("status"))

        output_truncated = d.pop("output_truncated")

        stdout = d.pop("stdout")

        stderr = d.pop("stderr")

        requested_by = d.pop("requested_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        shell = d.pop("shell", UNSET)

        exit_code = d.pop("exit_code", UNSET)

        _failure = d.pop("failure", UNSET)
        failure: AppForkExecResponseFailure | Unset
        if isinstance(_failure, Unset):
            failure = UNSET
        else:
            failure = AppForkExecResponseFailure.from_dict(_failure)

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

        app_fork_exec_response = cls(
            id=id,
            fork_id=fork_id,
            command=command,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
            status=status,
            output_truncated=output_truncated,
            stdout=stdout,
            stderr=stderr,
            requested_by=requested_by,
            created_at=created_at,
            shell=shell,
            exit_code=exit_code,
            failure=failure,
            started_at=started_at,
            finished_at=finished_at,
        )

        app_fork_exec_response.additional_properties = d
        return app_fork_exec_response

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
