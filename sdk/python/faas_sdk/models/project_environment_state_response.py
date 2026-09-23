from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_config_response import ProjectEnvironmentConfigResponse
    from ..models.project_environment_shared_resource_response import ProjectEnvironmentSharedResourceResponse
    from ..models.project_environment_state_workload_response import ProjectEnvironmentStateWorkloadResponse


T = TypeVar("T", bound="ProjectEnvironmentStateResponse")


@_attrs_define
class ProjectEnvironmentStateResponse:
    """Point-in-time effective state snapshot for a project environment."""

    project_slug: str
    environment: str
    protected: bool
    configuration: ProjectEnvironmentConfigResponse
    """Latest immutable non-secret configuration snapshot for a project environment."""
    workloads: list[ProjectEnvironmentStateWorkloadResponse]
    shared_resources: list[ProjectEnvironmentSharedResourceResponse]
    generated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        environment = self.environment

        protected = self.protected

        configuration = self.configuration.to_dict()

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        shared_resources = []
        for shared_resources_item_data in self.shared_resources:
            shared_resources_item = shared_resources_item_data.to_dict()
            shared_resources.append(shared_resources_item)

        generated_at = self.generated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "environment": environment,
                "protected": protected,
                "configuration": configuration,
                "workloads": workloads,
                "shared_resources": shared_resources,
                "generated_at": generated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_config_response import ProjectEnvironmentConfigResponse
        from ..models.project_environment_shared_resource_response import ProjectEnvironmentSharedResourceResponse
        from ..models.project_environment_state_workload_response import ProjectEnvironmentStateWorkloadResponse

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        environment = d.pop("environment")

        protected = d.pop("protected")

        configuration = ProjectEnvironmentConfigResponse.from_dict(d.pop("configuration"))

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = ProjectEnvironmentStateWorkloadResponse.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        shared_resources = []
        _shared_resources = d.pop("shared_resources")
        for shared_resources_item_data in _shared_resources:
            shared_resources_item = ProjectEnvironmentSharedResourceResponse.from_dict(shared_resources_item_data)

            shared_resources.append(shared_resources_item)

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        project_environment_state_response = cls(
            project_slug=project_slug,
            environment=environment,
            protected=protected,
            configuration=configuration,
            workloads=workloads,
            shared_resources=shared_resources,
            generated_at=generated_at,
        )

        project_environment_state_response.additional_properties = d
        return project_environment_state_response

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
