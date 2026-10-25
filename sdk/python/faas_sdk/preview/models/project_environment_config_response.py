from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_config_response_values import ProjectEnvironmentConfigResponseValues


T = TypeVar("T", bound="ProjectEnvironmentConfigResponse")


@_attrs_define
class ProjectEnvironmentConfigResponse:
    """Latest immutable non-secret configuration snapshot for a project environment."""

    project_slug: str
    environment: str
    version: int
    config_hash: str
    values: ProjectEnvironmentConfigResponseValues
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        environment = self.environment

        version = self.version

        config_hash = self.config_hash

        values = self.values.to_dict()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "environment": environment,
                "version": version,
                "config_hash": config_hash,
                "values": values,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_config_response_values import ProjectEnvironmentConfigResponseValues

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        environment = d.pop("environment")

        version = d.pop("version")

        config_hash = d.pop("config_hash")

        values = ProjectEnvironmentConfigResponseValues.from_dict(d.pop("values"))

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        project_environment_config_response = cls(
            project_slug=project_slug,
            environment=environment,
            version=version,
            config_hash=config_hash,
            values=values,
            updated_at=updated_at,
        )

        project_environment_config_response.additional_properties = d
        return project_environment_config_response

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
