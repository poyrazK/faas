from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_clone_response_shared_resources_item import (
    ProjectEnvironmentCloneResponseSharedResourcesItem,
    check_project_environment_clone_response_shared_resources_item,
)

T = TypeVar("T", bound="ProjectEnvironmentCloneResponse")


@_attrs_define
class ProjectEnvironmentCloneResponse:
    """Non-secret copy counts for an environment clone. Managed database or bucket data appears as shared only after
    explicit opt-in.

    """

    configuration_copied: bool
    variables_copied: int
    secrets_copied: int
    workloads_copied: int
    bindings_copied: int
    shared_resources: list[ProjectEnvironmentCloneResponseSharedResourcesItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        configuration_copied = self.configuration_copied

        variables_copied = self.variables_copied

        secrets_copied = self.secrets_copied

        workloads_copied = self.workloads_copied

        bindings_copied = self.bindings_copied

        shared_resources = []
        for shared_resources_item_data in self.shared_resources:
            shared_resources_item: str = shared_resources_item_data
            shared_resources.append(shared_resources_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "configuration_copied": configuration_copied,
                "variables_copied": variables_copied,
                "secrets_copied": secrets_copied,
                "workloads_copied": workloads_copied,
                "bindings_copied": bindings_copied,
                "shared_resources": shared_resources,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        configuration_copied = d.pop("configuration_copied")

        variables_copied = d.pop("variables_copied")

        secrets_copied = d.pop("secrets_copied")

        workloads_copied = d.pop("workloads_copied")

        bindings_copied = d.pop("bindings_copied")

        shared_resources = []
        _shared_resources = d.pop("shared_resources")
        for shared_resources_item_data in _shared_resources:
            shared_resources_item = check_project_environment_clone_response_shared_resources_item(
                shared_resources_item_data
            )

            shared_resources.append(shared_resources_item)

        project_environment_clone_response = cls(
            configuration_copied=configuration_copied,
            variables_copied=variables_copied,
            secrets_copied=secrets_copied,
            workloads_copied=workloads_copied,
            bindings_copied=bindings_copied,
            shared_resources=shared_resources,
        )

        project_environment_clone_response.additional_properties = d
        return project_environment_clone_response

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
