from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="GitHubDeploymentPolicy")


@_attrs_define
class GitHubDeploymentPolicy:
    """Customer-owned project-level GitHub deployment behaviour."""

    project_id: UUID
    root_dir: str
    """Repository-relative root used when the project has a root workload."""
    ignored_paths: list[str]
    """Exact paths, one-segment globs, or trailing /** directory patterns that do not trigger builds."""
    preview_enabled: bool = True
    preview_ttl_hours: int = 168
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_id = str(self.project_id)

        root_dir = self.root_dir

        ignored_paths = self.ignored_paths

        preview_enabled = self.preview_enabled

        preview_ttl_hours = self.preview_ttl_hours

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_id": project_id,
                "root_dir": root_dir,
                "ignored_paths": ignored_paths,
                "preview_enabled": preview_enabled,
                "preview_ttl_hours": preview_ttl_hours,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        project_id = UUID(d.pop("project_id"))

        root_dir = d.pop("root_dir")

        ignored_paths = cast(list[str], d.pop("ignored_paths"))

        preview_enabled = d.pop("preview_enabled")

        preview_ttl_hours = d.pop("preview_ttl_hours")

        git_hub_deployment_policy = cls(
            project_id=project_id,
            root_dir=root_dir,
            ignored_paths=ignored_paths,
            preview_enabled=preview_enabled,
            preview_ttl_hours=preview_ttl_hours,
        )

        git_hub_deployment_policy.additional_properties = d
        return git_hub_deployment_policy

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
