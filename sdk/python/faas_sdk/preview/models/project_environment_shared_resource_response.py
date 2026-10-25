from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_shared_resource_response_kind import (
    ProjectEnvironmentSharedResourceResponseKind,
    check_project_environment_shared_resource_response_kind,
)
from ..models.project_environment_shared_resource_response_ownership import (
    ProjectEnvironmentSharedResourceResponseOwnership,
    check_project_environment_shared_resource_response_ownership,
)

T = TypeVar("T", bound="ProjectEnvironmentSharedResourceResponse")


@_attrs_define
class ProjectEnvironmentSharedResourceResponse:
    """Application-scoped resource that is shared by all environments."""

    kind: ProjectEnvironmentSharedResourceResponseKind
    ownership: ProjectEnvironmentSharedResourceResponseOwnership
    note: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        ownership: str = self.ownership

        note = self.note

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "ownership": ownership,
                "note": note,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_project_environment_shared_resource_response_kind(d.pop("kind"))

        ownership = check_project_environment_shared_resource_response_ownership(d.pop("ownership"))

        note = d.pop("note")

        project_environment_shared_resource_response = cls(
            kind=kind,
            ownership=ownership,
            note=note,
        )

        project_environment_shared_resource_response.additional_properties = d
        return project_environment_shared_resource_response

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
