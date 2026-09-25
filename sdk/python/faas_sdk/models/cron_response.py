from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.cron_response_kind import CronResponseKind, check_cron_response_kind
from ..models.cron_response_suspended_reason import CronResponseSuspendedReason, check_cron_response_suspended_reason
from ..types import UNSET, Unset

T = TypeVar("T", bound="CronResponse")


@_attrs_define
class CronResponse:
    """An app schedule: either an HTTP-path cron or a deployment-attached command cron."""

    id: str
    app_id: str
    kind: CronResponseKind
    """HTTP triggers the app path; command runs argv in a fresh VM using the live deployment selected at fire time."""
    schedule: str
    enabled: bool
    timezone: str
    """IANA timezone used to evaluate the schedule; defaults to UTC."""
    skip_if_running: bool
    """When true, consume a scheduled occurrence while a prior HTTP invocation or app command task is active."""
    created_at: datetime.datetime
    path: str | Unset = UNSET
    """HTTP target path; omitted for command crons."""
    command: list[str] | Unset = UNSET
    """Direct command argv; present only for command crons."""
    command_shell: bool | Unset = UNSET
    """Interpret a one-element command through the app shell; false executes argv directly."""
    timeout_seconds: int | Unset = UNSET
    """Per-fire command deadline."""
    max_output_bytes: int | Unset = UNSET
    """Combined stdout/stderr tail cap for command runs."""
    suspended_reason: CronResponseSuspendedReason | Unset = UNSET
    """Why an enabled schedule is paused. Redeploy the app successfully to clear no_live_deployment."""
    last_fired_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        kind: str = self.kind

        schedule = self.schedule

        enabled = self.enabled

        timezone = self.timezone

        skip_if_running = self.skip_if_running

        created_at = self.created_at.isoformat()

        path = self.path

        command: list[str] | Unset = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        command_shell = self.command_shell

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        suspended_reason: str | Unset = UNSET
        if not isinstance(self.suspended_reason, Unset):
            suspended_reason = self.suspended_reason

        last_fired_at: None | str | Unset
        if isinstance(self.last_fired_at, Unset):
            last_fired_at = UNSET
        elif isinstance(self.last_fired_at, datetime.datetime):
            last_fired_at = self.last_fired_at.isoformat()
        else:
            last_fired_at = self.last_fired_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "kind": kind,
                "schedule": schedule,
                "enabled": enabled,
                "timezone": timezone,
                "skip_if_running": skip_if_running,
                "created_at": created_at,
            }
        )
        if path is not UNSET:
            field_dict["path"] = path
        if command is not UNSET:
            field_dict["command"] = command
        if command_shell is not UNSET:
            field_dict["command_shell"] = command_shell
        if timeout_seconds is not UNSET:
            field_dict["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not UNSET:
            field_dict["max_output_bytes"] = max_output_bytes
        if suspended_reason is not UNSET:
            field_dict["suspended_reason"] = suspended_reason
        if last_fired_at is not UNSET:
            field_dict["last_fired_at"] = last_fired_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        kind = check_cron_response_kind(d.pop("kind"))

        schedule = d.pop("schedule")

        enabled = d.pop("enabled")

        timezone = d.pop("timezone")

        skip_if_running = d.pop("skip_if_running")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        path = d.pop("path", UNSET)

        command = cast(list[str], d.pop("command", UNSET))

        command_shell = d.pop("command_shell", UNSET)

        timeout_seconds = d.pop("timeout_seconds", UNSET)

        max_output_bytes = d.pop("max_output_bytes", UNSET)

        _suspended_reason = d.pop("suspended_reason", UNSET)
        suspended_reason: CronResponseSuspendedReason | Unset
        if isinstance(_suspended_reason, Unset):
            suspended_reason = UNSET
        else:
            suspended_reason = check_cron_response_suspended_reason(_suspended_reason)

        def _parse_last_fired_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_fired_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_fired_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_fired_at = _parse_last_fired_at(d.pop("last_fired_at", UNSET))

        cron_response = cls(
            id=id,
            app_id=app_id,
            kind=kind,
            schedule=schedule,
            enabled=enabled,
            timezone=timezone,
            skip_if_running=skip_if_running,
            created_at=created_at,
            path=path,
            command=command,
            command_shell=command_shell,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
            suspended_reason=suspended_reason,
            last_fired_at=last_fired_at,
        )

        cron_response.additional_properties = d
        return cron_response

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
