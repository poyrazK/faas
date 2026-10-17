from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.plan_workload import PlanWorkload


T = TypeVar("T", bound="ProjectDeploymentCheckWorkload")


@_attrs_define
class ProjectDeploymentCheckWorkload:
    """Planned workload and provided key names used by metadata-only deployment readiness checks."""

    workload: PlanWorkload
    """One discovered unit of work. Mirrors reposcan.Workload."""
    command_shell: bool | Unset = UNSET
    release_command: list[str] | Unset = UNSET
    release_command_shell: bool | Unset = UNSET
    provided_keys: list[str] | Unset = UNSET
    """Key names proposed by a separate secrets input; values are never included."""

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload.to_dict()

        command_shell = self.command_shell

        release_command: list[str] | Unset = UNSET
        if not isinstance(self.release_command, Unset):
            release_command = self.release_command

        release_command_shell = self.release_command_shell

        provided_keys: list[str] | Unset = UNSET
        if not isinstance(self.provided_keys, Unset):
            provided_keys = self.provided_keys

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workload": workload,
            }
        )
        if command_shell is not UNSET:
            field_dict["command_shell"] = command_shell
        if release_command is not UNSET:
            field_dict["release_command"] = release_command
        if release_command_shell is not UNSET:
            field_dict["release_command_shell"] = release_command_shell
        if provided_keys is not UNSET:
            field_dict["provided_keys"] = provided_keys

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.plan_workload import PlanWorkload

        d = dict(src_dict)
        workload = PlanWorkload.from_dict(d.pop("workload"))

        command_shell = d.pop("command_shell", UNSET)

        release_command = cast(list[str], d.pop("release_command", UNSET))

        release_command_shell = d.pop("release_command_shell", UNSET)

        provided_keys = cast(list[str], d.pop("provided_keys", UNSET))

        project_deployment_check_workload = cls(
            workload=workload,
            command_shell=command_shell,
            release_command=release_command,
            release_command_shell=release_command_shell,
            provided_keys=provided_keys,
        )

        return project_deployment_check_workload
