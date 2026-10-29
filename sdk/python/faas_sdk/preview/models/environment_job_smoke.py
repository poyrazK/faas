from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentJobSmoke")


@_attrs_define
class EnvironmentJobSmoke:
    """Reviewed argv-only command and short timeout for a job qualification attempt. The contract is frozen with the
    candidate; isolated execution and exit evidence are not yet available.

    """

    command: list[str]
    """Executable and arguments passed directly without a shell."""
    timeout_seconds: int
    """Explicit qualification wall-clock limit."""

    def to_dict(self) -> dict[str, Any]:
        command = self.command

        timeout_seconds = self.timeout_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command": command,
                "timeout_seconds": timeout_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        command = cast(list[str], d.pop("command"))

        timeout_seconds = d.pop("timeout_seconds")

        environment_job_smoke = cls(
            command=command,
            timeout_seconds=timeout_seconds,
        )

        return environment_job_smoke
