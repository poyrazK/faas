from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_shared_secret import ProjectEnvironmentSharedSecret


T = TypeVar("T", bound="ProjectEnvironmentSharedSecretResponse")


@_attrs_define
class ProjectEnvironmentSharedSecretResponse:
    """A shared secret's metadata after a write."""

    project_slug: str
    environment: str
    secret: ProjectEnvironmentSharedSecret
    """Metadata of one secret shared by every service of a project environment. The value is write-only."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        environment = self.environment

        secret = self.secret.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "environment": environment,
                "secret": secret,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_shared_secret import ProjectEnvironmentSharedSecret

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        environment = d.pop("environment")

        secret = ProjectEnvironmentSharedSecret.from_dict(d.pop("secret"))

        project_environment_shared_secret_response = cls(
            project_slug=project_slug,
            environment=environment,
            secret=secret,
        )

        project_environment_shared_secret_response.additional_properties = d
        return project_environment_shared_secret_response

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
