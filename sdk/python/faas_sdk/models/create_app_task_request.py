from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppTaskRequest")


@_attrs_define
class CreateAppTaskRequest:
    """One manual command to execute against the app's live deployment.
    `command_shell=false` executes argv directly. Shell mode requires one
    command string and is explicit so clients preserve quoting semantics.

    """

    command: list[str]
    command_shell: bool | Unset = False
    timeout_seconds: int | Unset = UNSET
    """Zero uses the 600-second default."""
    max_output_bytes: int | Unset = UNSET
    """Zero uses the 1 MiB default; non-zero values must be at least 1024."""

    def to_dict(self) -> dict[str, Any]:
        command = self.command

        command_shell = self.command_shell

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command": command,
            }
        )
        if command_shell is not UNSET:
            field_dict["command_shell"] = command_shell
        if timeout_seconds is not UNSET:
            field_dict["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not UNSET:
            field_dict["max_output_bytes"] = max_output_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        command = cast(list[str], d.pop("command"))

        command_shell = d.pop("command_shell", UNSET)

        timeout_seconds = d.pop("timeout_seconds", UNSET)

        max_output_bytes = d.pop("max_output_bytes", UNSET)

        create_app_task_request = cls(
            command=command,
            command_shell=command_shell,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
        )

        return create_app_task_request
