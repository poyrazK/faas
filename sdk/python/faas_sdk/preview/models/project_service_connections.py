from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_service_connections_additional_property import ProjectServiceConnectionsAdditionalProperty


T = TypeVar("T", bound="ProjectServiceConnections")


@_attrs_define
class ProjectServiceConnections:
    """Selected caller names mapped to variable-to-service choices. Targets must be selected declared HTTP or GraphQL
    dependencies. service_connections uses private runtime URLs; public_service_connections uses reviewed public build
    URLs.

    """

    additional_properties: dict[str, ProjectServiceConnectionsAdditionalProperty] = _attrs_field(
        init=False, factory=dict
    )

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_service_connections_additional_property import ProjectServiceConnectionsAdditionalProperty

        d = dict(src_dict)
        project_service_connections = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = ProjectServiceConnectionsAdditionalProperty.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        project_service_connections.additional_properties = additional_properties
        return project_service_connections

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> ProjectServiceConnectionsAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: ProjectServiceConnectionsAdditionalProperty) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
