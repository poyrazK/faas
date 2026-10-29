from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_public_build_ur_ls_additional_property import ProjectPublicBuildURLsAdditionalProperty


T = TypeVar("T", bound="ProjectPublicBuildURLs")


@_attrs_define
class ProjectPublicBuildURLs:
    """Explicit public HTTPS build URLs for selected services, persisted and exposed in frontend bundles. Keys use VITE_,
    NEXT_PUBLIC_, REACT_APP_, PUBLIC_, NUXT_PUBLIC_ or GATSBY_ and end in _URL. URLs may include a base path but no
    credentials, query, fragment, IP, localhost or private service name. Up to ten URLs per service. Omitted keys keep
    their existing runtime configuration.

    """

    additional_properties: dict[str, ProjectPublicBuildURLsAdditionalProperty] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_public_build_ur_ls_additional_property import ProjectPublicBuildURLsAdditionalProperty

        d = dict(src_dict)
        project_public_build_ur_ls = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = ProjectPublicBuildURLsAdditionalProperty.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        project_public_build_ur_ls.additional_properties = additional_properties
        return project_public_build_ur_ls

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> ProjectPublicBuildURLsAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: ProjectPublicBuildURLsAdditionalProperty) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
