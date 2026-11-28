from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_release_workload_response import ProjectEnvironmentReleaseWorkloadResponse


T = TypeVar("T", bound="ProjectEnvironmentReleaseListResponse")


@_attrs_define
class ProjectEnvironmentReleaseListResponse:
    """Current non-secret release inventory for a project environment."""

    project_slug: str
    environment: str
    workloads: list[ProjectEnvironmentReleaseWorkloadResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        environment = self.environment

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "environment": environment,
                "workloads": workloads,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_release_workload_response import ProjectEnvironmentReleaseWorkloadResponse

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        environment = d.pop("environment")

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = ProjectEnvironmentReleaseWorkloadResponse.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        project_environment_release_list_response = cls(
            project_slug=project_slug,
            environment=environment,
            workloads=workloads,
        )

        project_environment_release_list_response.additional_properties = d
        return project_environment_release_list_response

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
