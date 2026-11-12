from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_branch_summary_data import (
    ProjectEnvironmentBranchSummaryData,
    check_project_environment_branch_summary_data,
)
from ..models.project_environment_branch_summary_private_networks import (
    ProjectEnvironmentBranchSummaryPrivateNetworks,
    check_project_environment_branch_summary_private_networks,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentBranchSummary")


@_attrs_define
class ProjectEnvironmentBranchSummary:
    """One environment branch in a list."""

    slug: str
    parent: str
    branch_point: datetime.datetime
    expires_at: datetime.datetime
    egress: str
    private_networks: ProjectEnvironmentBranchSummaryPrivateNetworks
    depth: int
    data: ProjectEnvironmentBranchSummaryData
    created_at: datetime.datetime
    data_error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        parent = self.parent

        branch_point = self.branch_point.isoformat()

        expires_at = self.expires_at.isoformat()

        egress = self.egress

        private_networks: str = self.private_networks

        depth = self.depth

        data: str = self.data

        created_at = self.created_at.isoformat()

        data_error = self.data_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "parent": parent,
                "branch_point": branch_point,
                "expires_at": expires_at,
                "egress": egress,
                "private_networks": private_networks,
                "depth": depth,
                "data": data,
                "created_at": created_at,
            }
        )
        if data_error is not UNSET:
            field_dict["data_error"] = data_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        parent = d.pop("parent")

        branch_point = datetime.datetime.fromisoformat(d.pop("branch_point"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        egress = d.pop("egress")

        private_networks = check_project_environment_branch_summary_private_networks(d.pop("private_networks"))

        depth = d.pop("depth")

        data = check_project_environment_branch_summary_data(d.pop("data"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        data_error = d.pop("data_error", UNSET)

        project_environment_branch_summary = cls(
            slug=slug,
            parent=parent,
            branch_point=branch_point,
            expires_at=expires_at,
            egress=egress,
            private_networks=private_networks,
            depth=depth,
            data=data,
            created_at=created_at,
            data_error=data_error,
        )

        project_environment_branch_summary.additional_properties = d
        return project_environment_branch_summary

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
