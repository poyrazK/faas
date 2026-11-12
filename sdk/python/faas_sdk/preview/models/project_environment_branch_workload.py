from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentBranchWorkload")


@_attrs_define
class ProjectEnvironmentBranchWorkload:
    """One workload the branch published from its parent's build. It starts on its first request."""

    workload: str
    deployment_id: str
    source_deployment_id: str
    """The parent deployment whose build the branch reuses."""
    status: str
    url: str | Unset = UNSET
    """The workload's address in the branch. Its first request starts it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload

        deployment_id = self.deployment_id

        source_deployment_id = self.source_deployment_id

        status = self.status

        url = self.url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload": workload,
                "deployment_id": deployment_id,
                "source_deployment_id": source_deployment_id,
                "status": status,
            }
        )
        if url is not UNSET:
            field_dict["url"] = url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload = d.pop("workload")

        deployment_id = d.pop("deployment_id")

        source_deployment_id = d.pop("source_deployment_id")

        status = d.pop("status")

        url = d.pop("url", UNSET)

        project_environment_branch_workload = cls(
            workload=workload,
            deployment_id=deployment_id,
            source_deployment_id=source_deployment_id,
            status=status,
            url=url,
        )

        project_environment_branch_workload.additional_properties = d
        return project_environment_branch_workload

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
