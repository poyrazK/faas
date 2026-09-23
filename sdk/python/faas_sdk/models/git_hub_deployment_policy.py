from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.git_hub_deployment_policy_preview_service_policy import (
    GitHubDeploymentPolicyPreviewServicePolicy,
    check_git_hub_deployment_policy_preview_service_policy,
)

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
    preview_service_policy: GitHubDeploymentPolicyPreviewServicePolicy = "deny"
    """Controls calls from project previews to production internal
    services. `deny` rejects the call before discovery or wake-up;
    `allow_marked` permits it and marks the request as preview-origin
    traffic. Projects created before this policy was introduced are
    migration-backed to `allow_marked`.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_id = str(self.project_id)

        root_dir = self.root_dir

        ignored_paths = self.ignored_paths

        preview_enabled = self.preview_enabled

        preview_ttl_hours = self.preview_ttl_hours

        preview_service_policy: str = self.preview_service_policy

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_id": project_id,
                "root_dir": root_dir,
                "ignored_paths": ignored_paths,
                "preview_enabled": preview_enabled,
                "preview_ttl_hours": preview_ttl_hours,
                "preview_service_policy": preview_service_policy,
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

        preview_service_policy = check_git_hub_deployment_policy_preview_service_policy(d.pop("preview_service_policy"))

        git_hub_deployment_policy = cls(
            project_id=project_id,
            root_dir=root_dir,
            ignored_paths=ignored_paths,
            preview_enabled=preview_enabled,
            preview_ttl_hours=preview_ttl_hours,
            preview_service_policy=preview_service_policy,
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
