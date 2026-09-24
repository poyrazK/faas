from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_binding_change_response import ProjectEnvironmentBindingChangeResponse
    from ..models.project_environment_edge_policy_diff_response import ProjectEnvironmentEdgePolicyDiffResponse
    from ..models.project_environment_release_diff_response import ProjectEnvironmentReleaseDiffResponse
    from ..models.project_environment_route_policy_diff_response import ProjectEnvironmentRoutePolicyDiffResponse
    from ..models.project_environment_secret_change_response import ProjectEnvironmentSecretChangeResponse
    from ..models.project_environment_variable_change_response import ProjectEnvironmentVariableChangeResponse


T = TypeVar("T", bound="ProjectEnvironmentWorkloadDiffResponse")


@_attrs_define
class ProjectEnvironmentWorkloadDiffResponse:
    """Release, variable, secret, and binding changes for one workload."""

    workload_slug: str
    workload_name: str
    release: ProjectEnvironmentReleaseDiffResponse
    """Before-and-after release state for a workload in an environment diff."""
    variables: list[ProjectEnvironmentVariableChangeResponse]
    secrets: list[ProjectEnvironmentSecretChangeResponse]
    bindings: list[ProjectEnvironmentBindingChangeResponse]
    routes: ProjectEnvironmentRoutePolicyDiffResponse
    """Difference in the effective declared-route contract or its ownership."""
    policies: ProjectEnvironmentEdgePolicyDiffResponse
    """Difference in one environment edge-policy group or its ownership."""
    routing_policies: ProjectEnvironmentEdgePolicyDiffResponse
    """Difference in one environment edge-policy group or its ownership."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload_slug = self.workload_slug

        workload_name = self.workload_name

        release = self.release.to_dict()

        variables = []
        for variables_item_data in self.variables:
            variables_item = variables_item_data.to_dict()
            variables.append(variables_item)

        secrets = []
        for secrets_item_data in self.secrets:
            secrets_item = secrets_item_data.to_dict()
            secrets.append(secrets_item)

        bindings = []
        for bindings_item_data in self.bindings:
            bindings_item = bindings_item_data.to_dict()
            bindings.append(bindings_item)

        routes = self.routes.to_dict()

        policies = self.policies.to_dict()

        routing_policies = self.routing_policies.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload_slug": workload_slug,
                "workload_name": workload_name,
                "release": release,
                "variables": variables,
                "secrets": secrets,
                "bindings": bindings,
                "routes": routes,
                "policies": policies,
                "routing_policies": routing_policies,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_binding_change_response import ProjectEnvironmentBindingChangeResponse
        from ..models.project_environment_edge_policy_diff_response import ProjectEnvironmentEdgePolicyDiffResponse
        from ..models.project_environment_release_diff_response import ProjectEnvironmentReleaseDiffResponse
        from ..models.project_environment_route_policy_diff_response import ProjectEnvironmentRoutePolicyDiffResponse
        from ..models.project_environment_secret_change_response import ProjectEnvironmentSecretChangeResponse
        from ..models.project_environment_variable_change_response import ProjectEnvironmentVariableChangeResponse

        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        workload_name = d.pop("workload_name")

        release = ProjectEnvironmentReleaseDiffResponse.from_dict(d.pop("release"))

        variables = []
        _variables = d.pop("variables")
        for variables_item_data in _variables:
            variables_item = ProjectEnvironmentVariableChangeResponse.from_dict(variables_item_data)

            variables.append(variables_item)

        secrets = []
        _secrets = d.pop("secrets")
        for secrets_item_data in _secrets:
            secrets_item = ProjectEnvironmentSecretChangeResponse.from_dict(secrets_item_data)

            secrets.append(secrets_item)

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = ProjectEnvironmentBindingChangeResponse.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        routes = ProjectEnvironmentRoutePolicyDiffResponse.from_dict(d.pop("routes"))

        policies = ProjectEnvironmentEdgePolicyDiffResponse.from_dict(d.pop("policies"))

        routing_policies = ProjectEnvironmentEdgePolicyDiffResponse.from_dict(d.pop("routing_policies"))

        project_environment_workload_diff_response = cls(
            workload_slug=workload_slug,
            workload_name=workload_name,
            release=release,
            variables=variables,
            secrets=secrets,
            bindings=bindings,
            routes=routes,
            policies=policies,
            routing_policies=routing_policies,
        )

        project_environment_workload_diff_response.additional_properties = d
        return project_environment_workload_diff_response

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
