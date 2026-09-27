from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_state_response_release_set_status import (
    ProjectEnvironmentStateResponseReleaseSetStatus,
    check_project_environment_state_response_release_set_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_config_response import ProjectEnvironmentConfigResponse
    from ..models.project_environment_shared_resource_response import ProjectEnvironmentSharedResourceResponse
    from ..models.project_environment_state_workload_response import ProjectEnvironmentStateWorkloadResponse
    from ..models.project_release_set_response import ProjectReleaseSetResponse


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
    active_release_set: None | ProjectReleaseSetResponse | Unset = UNSET
    """Active graph, or null when no graph has been published. Its selected members may differ from the per-
    workload live deployments below."""
    release_set_status: ProjectEnvironmentStateResponseReleaseSetStatus | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.project_release_set_response import ProjectReleaseSetResponse

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

        active_release_set: dict[str, Any] | None | Unset
        if isinstance(self.active_release_set, Unset):
            active_release_set = UNSET
        elif isinstance(self.active_release_set, ProjectReleaseSetResponse):
            active_release_set = self.active_release_set.to_dict()
        else:
            active_release_set = self.active_release_set

        release_set_status: str | Unset = UNSET
        if not isinstance(self.release_set_status, Unset):
            release_set_status = self.release_set_status

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
        if active_release_set is not UNSET:
            field_dict["active_release_set"] = active_release_set
        if release_set_status is not UNSET:
            field_dict["release_set_status"] = release_set_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_config_response import ProjectEnvironmentConfigResponse
        from ..models.project_environment_shared_resource_response import ProjectEnvironmentSharedResourceResponse
        from ..models.project_environment_state_workload_response import ProjectEnvironmentStateWorkloadResponse
        from ..models.project_release_set_response import ProjectReleaseSetResponse

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

        def _parse_active_release_set(data: object) -> None | ProjectReleaseSetResponse | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                active_release_set_type_0 = ProjectReleaseSetResponse.from_dict(data)

                return active_release_set_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ProjectReleaseSetResponse | Unset, data)

        active_release_set = _parse_active_release_set(d.pop("active_release_set", UNSET))

        _release_set_status = d.pop("release_set_status", UNSET)
        release_set_status: ProjectEnvironmentStateResponseReleaseSetStatus | Unset
        if isinstance(_release_set_status, Unset):
            release_set_status = UNSET
        else:
            release_set_status = check_project_environment_state_response_release_set_status(_release_set_status)

        project_environment_state_response = cls(
            project_slug=project_slug,
            environment=environment,
            protected=protected,
            configuration=configuration,
            workloads=workloads,
            shared_resources=shared_resources,
            generated_at=generated_at,
            active_release_set=active_release_set,
            release_set_status=release_set_status,
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
