from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppForkExecRequest")


@_attrs_define
class CreateAppForkExecRequest:
    """Body of `POST /v1/apps/{slug}/forks/{id}/execs`."""

    command: list[str]
    """The argv to run. With `shell`, exactly one string run by the app's shell."""
    shell: bool | Unset = UNSET
    """Run the single command string through the app's shell."""
    timeout_seconds: int | Unset = UNSET
    """Default 60."""
    max_output_bytes: int | Unset = UNSET
    """Combined stdout/stderr kept (the tail). Default 65536."""

    def to_dict(self) -> dict[str, Any]:
        command = self.command

        shell = self.shell

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command": command,
            }
        )
        if shell is not UNSET:
            field_dict["shell"] = shell
        if timeout_seconds is not UNSET:
            field_dict["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not UNSET:
            field_dict["max_output_bytes"] = max_output_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        command = cast(list[str], d.pop("command"))

        shell = d.pop("shell", UNSET)

        timeout_seconds = d.pop("timeout_seconds", UNSET)

        max_output_bytes = d.pop("max_output_bytes", UNSET)

        create_app_fork_exec_request = cls(
            command=command,
            shell=shell,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
        )

        return create_app_fork_exec_request
