from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SeedDevSessionFromEnvironmentBranchRequest")


@_attrs_define
class SeedDevSessionFromEnvironmentBranchRequest:
    """Deploys one workload of a simulated environment branch into a rehearsal member's developer session, reusing the
    branch's build.

    """

    workspace_id: str
    """The rehearsal run's workspace ID."""
    project: str
    """Slug of the project that owns the branch."""
    environment: str
    """Slug of the simulated branch environment."""
    workload: str
    """Workload name in the project."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workspace_id = self.workspace_id

        project = self.project

        environment = self.environment

        workload = self.workload

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workspace_id": workspace_id,
                "project": project,
                "environment": environment,
                "workload": workload,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workspace_id = d.pop("workspace_id")

        project = d.pop("project")

        environment = d.pop("environment")

        workload = d.pop("workload")

        seed_dev_session_from_environment_branch_request = cls(
            workspace_id=workspace_id,
            project=project,
            environment=environment,
            workload=workload,
        )

        seed_dev_session_from_environment_branch_request.additional_properties = d
        return seed_dev_session_from_environment_branch_request

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
