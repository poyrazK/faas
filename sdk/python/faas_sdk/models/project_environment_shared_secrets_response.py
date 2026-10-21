from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_shared_secret import ProjectEnvironmentSharedSecret


T = TypeVar("T", bound="ProjectEnvironmentSharedSecretsResponse")


@_attrs_define
class ProjectEnvironmentSharedSecretsResponse:
    """The secrets a project environment shares with its services."""

    project_slug: str
    environment: str
    secrets: list[ProjectEnvironmentSharedSecret]
    limit: int
    """The plan's ProjectSecretCountMax for one environment."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        environment = self.environment

        secrets = []
        for secrets_item_data in self.secrets:
            secrets_item = secrets_item_data.to_dict()
            secrets.append(secrets_item)

        limit = self.limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "environment": environment,
                "secrets": secrets,
                "limit": limit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_shared_secret import ProjectEnvironmentSharedSecret

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        environment = d.pop("environment")

        secrets = []
        _secrets = d.pop("secrets")
        for secrets_item_data in _secrets:
            secrets_item = ProjectEnvironmentSharedSecret.from_dict(secrets_item_data)

            secrets.append(secrets_item)

        limit = d.pop("limit")

        project_environment_shared_secrets_response = cls(
            project_slug=project_slug,
            environment=environment,
            secrets=secrets,
            limit=limit,
        )

        project_environment_shared_secrets_response.additional_properties = d
        return project_environment_shared_secrets_response

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
