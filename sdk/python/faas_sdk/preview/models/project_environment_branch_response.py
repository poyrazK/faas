from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_branch_response_data import (
    ProjectEnvironmentBranchResponseData,
    check_project_environment_branch_response_data,
)
from ..models.project_environment_branch_response_private_networks import (
    ProjectEnvironmentBranchResponsePrivateNetworks,
    check_project_environment_branch_response_private_networks,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_branch_workload import ProjectEnvironmentBranchWorkload


T = TypeVar("T", bound="ProjectEnvironmentBranchResponse")


@_attrs_define
class ProjectEnvironmentBranchResponse:
    """How an environment branches from its parent."""

    parent: str
    """Slug of the parent environment."""
    branch_point: datetime.datetime
    """The moment the branch copied its parent."""
    expires_at: datetime.datetime
    """When the branch is deleted."""
    egress: str
    private_networks: ProjectEnvironmentBranchResponsePrivateNetworks
    """Whether the branch's instances join their workloads' private networks."""
    depth: int
    """1 for a branch of an ordinary environment"""
    data: ProjectEnvironmentBranchResponseData
    """Whether the copies of the parent's managed PostgreSQL databases exist. The branch's workloads start only
    when this is ready."""
    data_error: str | Unset = UNSET
    """Why copying the parent's data failed or is being retried."""
    workloads: list[ProjectEnvironmentBranchWorkload] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        parent = self.parent

        branch_point = self.branch_point.isoformat()

        expires_at = self.expires_at.isoformat()

        egress = self.egress

        private_networks: str = self.private_networks

        depth = self.depth

        data: str = self.data

        data_error = self.data_error

        workloads: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workloads, Unset):
            workloads = []
            for workloads_item_data in self.workloads:
                workloads_item = workloads_item_data.to_dict()
                workloads.append(workloads_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "parent": parent,
                "branch_point": branch_point,
                "expires_at": expires_at,
                "egress": egress,
                "private_networks": private_networks,
                "depth": depth,
                "data": data,
            }
        )
        if data_error is not UNSET:
            field_dict["data_error"] = data_error
        if workloads is not UNSET:
            field_dict["workloads"] = workloads

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_branch_workload import ProjectEnvironmentBranchWorkload

        d = dict(src_dict)
        parent = d.pop("parent")

        branch_point = datetime.datetime.fromisoformat(d.pop("branch_point"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        egress = d.pop("egress")

        private_networks = check_project_environment_branch_response_private_networks(d.pop("private_networks"))

        depth = d.pop("depth")

        data = check_project_environment_branch_response_data(d.pop("data"))

        data_error = d.pop("data_error", UNSET)

        _workloads = d.pop("workloads", UNSET)
        workloads: list[ProjectEnvironmentBranchWorkload] | Unset = UNSET
        if _workloads is not UNSET:
            workloads = []
            for workloads_item_data in _workloads:
                workloads_item = ProjectEnvironmentBranchWorkload.from_dict(workloads_item_data)

                workloads.append(workloads_item)

        project_environment_branch_response = cls(
            parent=parent,
            branch_point=branch_point,
            expires_at=expires_at,
            egress=egress,
            private_networks=private_networks,
            depth=depth,
            data=data,
            data_error=data_error,
            workloads=workloads,
        )

        project_environment_branch_response.additional_properties = d
        return project_environment_branch_response

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
