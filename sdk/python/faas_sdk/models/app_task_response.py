from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_task_response_kind import AppTaskResponseKind, check_app_task_response_kind
from ..models.app_task_response_status import AppTaskResponseStatus, check_app_task_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_task_failure import AppTaskFailure


T = TypeVar("T", bound="AppTaskResponse")


@_attrs_define
class AppTaskResponse:
    """App-scoped receipt for a deployment-attached command. Scheduler lease
    data, rootfs storage keys, and image digests are intentionally omitted.

    """

    id: UUID
    app_id: str
    deployment_id: str
    deployment_scope: str
    kind: AppTaskResponseKind
    command: list[str]
    command_shell: bool
    status: AppTaskResponseStatus
    timeout_seconds: int
    max_output_bytes: int
    output_truncated: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    stdout_tail: str | Unset = UNSET
    stderr_tail: str | Unset = UNSET
    exit_code: int | None | Unset = UNSET
    failure: AppTaskFailure | None | Unset = UNSET
    cancel_requested_at: datetime.datetime | None | Unset = UNSET
    started_at: datetime.datetime | None | Unset = UNSET
    finished_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.app_task_failure import AppTaskFailure

        id = str(self.id)

        app_id = self.app_id

        deployment_id = self.deployment_id

        deployment_scope = self.deployment_scope

        kind: str = self.kind

        command = self.command

        command_shell = self.command_shell

        status: str = self.status

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        output_truncated = self.output_truncated

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        stdout_tail = self.stdout_tail

        stderr_tail = self.stderr_tail

        exit_code: int | None | Unset
        if isinstance(self.exit_code, Unset):
            exit_code = UNSET
        else:
            exit_code = self.exit_code

        failure: dict[str, Any] | None | Unset
        if isinstance(self.failure, Unset):
            failure = UNSET
        elif isinstance(self.failure, AppTaskFailure):
            failure = self.failure.to_dict()
        else:
            failure = self.failure

        cancel_requested_at: None | str | Unset
        if isinstance(self.cancel_requested_at, Unset):
            cancel_requested_at = UNSET
        elif isinstance(self.cancel_requested_at, datetime.datetime):
            cancel_requested_at = self.cancel_requested_at.isoformat()
        else:
            cancel_requested_at = self.cancel_requested_at

        started_at: None | str | Unset
        if isinstance(self.started_at, Unset):
            started_at = UNSET
        elif isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        finished_at: None | str | Unset
        if isinstance(self.finished_at, Unset):
            finished_at = UNSET
        elif isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "deployment_scope": deployment_scope,
                "kind": kind,
                "command": command,
                "command_shell": command_shell,
                "status": status,
                "timeout_seconds": timeout_seconds,
                "max_output_bytes": max_output_bytes,
                "output_truncated": output_truncated,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if stdout_tail is not UNSET:
            field_dict["stdout_tail"] = stdout_tail
        if stderr_tail is not UNSET:
            field_dict["stderr_tail"] = stderr_tail
        if exit_code is not UNSET:
            field_dict["exit_code"] = exit_code
        if failure is not UNSET:
            field_dict["failure"] = failure
        if cancel_requested_at is not UNSET:
            field_dict["cancel_requested_at"] = cancel_requested_at
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_task_failure import AppTaskFailure

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        deployment_scope = d.pop("deployment_scope")

        kind = check_app_task_response_kind(d.pop("kind"))

        command = cast(list[str], d.pop("command"))

        command_shell = d.pop("command_shell")

        status = check_app_task_response_status(d.pop("status"))

        timeout_seconds = d.pop("timeout_seconds")

        max_output_bytes = d.pop("max_output_bytes")

        output_truncated = d.pop("output_truncated")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        stdout_tail = d.pop("stdout_tail", UNSET)

        stderr_tail = d.pop("stderr_tail", UNSET)

        def _parse_exit_code(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        exit_code = _parse_exit_code(d.pop("exit_code", UNSET))

        def _parse_failure(data: object) -> AppTaskFailure | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                failure_type_0 = AppTaskFailure.from_dict(data)

                return failure_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(AppTaskFailure | None | Unset, data)

        failure = _parse_failure(d.pop("failure", UNSET))

        def _parse_cancel_requested_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                cancel_requested_at_type_0 = datetime.datetime.fromisoformat(data)

                return cancel_requested_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        cancel_requested_at = _parse_cancel_requested_at(d.pop("cancel_requested_at", UNSET))

        def _parse_started_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = datetime.datetime.fromisoformat(data)

                return started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        started_at = _parse_started_at(d.pop("started_at", UNSET))

        def _parse_finished_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                finished_at_type_0 = datetime.datetime.fromisoformat(data)

                return finished_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        finished_at = _parse_finished_at(d.pop("finished_at", UNSET))

        app_task_response = cls(
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            deployment_scope=deployment_scope,
            kind=kind,
            command=command,
            command_shell=command_shell,
            status=status,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
            output_truncated=output_truncated,
            created_at=created_at,
            updated_at=updated_at,
            stdout_tail=stdout_tail,
            stderr_tail=stderr_tail,
            exit_code=exit_code,
            failure=failure,
            cancel_requested_at=cancel_requested_at,
            started_at=started_at,
            finished_at=finished_at,
        )

        app_task_response.additional_properties = d
        return app_task_response

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
