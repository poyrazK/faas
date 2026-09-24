from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_binding_response import ProjectEnvironmentBindingResponse
    from ..models.project_environment_edge_policy_response import ProjectEnvironmentEdgePolicyResponse
    from ..models.project_environment_release_workload_response import ProjectEnvironmentReleaseWorkloadResponse
    from ..models.project_environment_route_policy_response import ProjectEnvironmentRoutePolicyResponse
    from ..models.project_environment_secret_response import ProjectEnvironmentSecretResponse
    from ..models.project_environment_variable_response import ProjectEnvironmentVariableResponse


T = TypeVar("T", bound="ProjectEnvironmentStateWorkloadResponse")


@_attrs_define
class ProjectEnvironmentStateWorkloadResponse:
    """Effective configuration and live release state for one workload."""

    workload_slug: str
    workload_name: str
    release: ProjectEnvironmentReleaseWorkloadResponse
    """Current live deployment metadata for one project workload. The stable environment URL is present before the
    first deployment but returns 404 until a live release exists."""
    variables: list[ProjectEnvironmentVariableResponse]
    secrets: list[ProjectEnvironmentSecretResponse]
    bindings: list[ProjectEnvironmentBindingResponse]
    routes: ProjectEnvironmentRoutePolicyResponse
    """Effective declared-route contract and whether it is environment-owned."""
    policies: ProjectEnvironmentEdgePolicyResponse
    """Ownership and rules for one independently replaceable edge-policy group."""
    routing_policies: ProjectEnvironmentEdgePolicyResponse
    """Ownership and rules for one independently replaceable edge-policy group."""
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
        from ..models.project_environment_binding_response import ProjectEnvironmentBindingResponse
        from ..models.project_environment_edge_policy_response import ProjectEnvironmentEdgePolicyResponse
        from ..models.project_environment_release_workload_response import ProjectEnvironmentReleaseWorkloadResponse
        from ..models.project_environment_route_policy_response import ProjectEnvironmentRoutePolicyResponse
        from ..models.project_environment_secret_response import ProjectEnvironmentSecretResponse
        from ..models.project_environment_variable_response import ProjectEnvironmentVariableResponse

        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        workload_name = d.pop("workload_name")

        release = ProjectEnvironmentReleaseWorkloadResponse.from_dict(d.pop("release"))

        variables = []
        _variables = d.pop("variables")
        for variables_item_data in _variables:
            variables_item = ProjectEnvironmentVariableResponse.from_dict(variables_item_data)

            variables.append(variables_item)

        secrets = []
        _secrets = d.pop("secrets")
        for secrets_item_data in _secrets:
            secrets_item = ProjectEnvironmentSecretResponse.from_dict(secrets_item_data)

            secrets.append(secrets_item)

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = ProjectEnvironmentBindingResponse.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        routes = ProjectEnvironmentRoutePolicyResponse.from_dict(d.pop("routes"))

        policies = ProjectEnvironmentEdgePolicyResponse.from_dict(d.pop("policies"))

        routing_policies = ProjectEnvironmentEdgePolicyResponse.from_dict(d.pop("routing_policies"))

        project_environment_state_workload_response = cls(
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

        project_environment_state_workload_response.additional_properties = d
        return project_environment_state_workload_response

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
