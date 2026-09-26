from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateCronRequest")


@_attrs_define
class CreateCronRequest:
    """Create an HTTP-path cron or deployment-attached app command schedule."""

    app_id: str
    """App id or slug."""
    schedule: str
    path: str | Unset = UNSET
    """HTTP target path; defaults to / and is mutually exclusive with command."""
    command: list[str] | Unset = UNSET
    """Direct command argv; mutually exclusive with path."""
    command_shell: bool | Unset = False
    """Interpret one command string through the app shell when true."""
    timeout_seconds: int | Unset = UNSET
    """Per-fire command deadline; zero uses the 600-second default."""
    max_output_bytes: int | Unset = UNSET
    """Combined stdout/stderr tail cap; zero uses the 1 MiB default."""
    enabled: bool | None | Unset = UNSET
    timezone: str | Unset = UNSET
    """IANA timezone; defaults to UTC."""
    skip_if_running: bool | None | Unset = UNSET
    """Skip a scheduled fire when an earlier cron invocation is still running."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        schedule = self.schedule

        path = self.path

        command: list[str] | Unset = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        command_shell = self.command_shell

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        enabled: bool | None | Unset
        if isinstance(self.enabled, Unset):
            enabled = UNSET
        else:
            enabled = self.enabled

        timezone = self.timezone

        skip_if_running: bool | None | Unset
        if isinstance(self.skip_if_running, Unset):
            skip_if_running = UNSET
        else:
            skip_if_running = self.skip_if_running

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "schedule": schedule,
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
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if timezone is not UNSET:
            field_dict["timezone"] = timezone
        if skip_if_running is not UNSET:
            field_dict["skip_if_running"] = skip_if_running

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        schedule = d.pop("schedule")

        path = d.pop("path", UNSET)

        command = cast(list[str], d.pop("command", UNSET))

        command_shell = d.pop("command_shell", UNSET)

        timeout_seconds = d.pop("timeout_seconds", UNSET)

        max_output_bytes = d.pop("max_output_bytes", UNSET)

        def _parse_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        enabled = _parse_enabled(d.pop("enabled", UNSET))

        timezone = d.pop("timezone", UNSET)

        def _parse_skip_if_running(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        skip_if_running = _parse_skip_if_running(d.pop("skip_if_running", UNSET))

        create_cron_request = cls(
            app_id=app_id,
            schedule=schedule,
            path=path,
            command=command,
            command_shell=command_shell,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
            enabled=enabled,
            timezone=timezone,
            skip_if_running=skip_if_running,
        )

        create_cron_request.additional_properties = d
        return create_cron_request

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
