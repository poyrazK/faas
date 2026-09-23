from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.git_hub_deployment_policy_patch_preview_service_policy import (
    GitHubDeploymentPolicyPatchPreviewServicePolicy,
    check_git_hub_deployment_policy_patch_preview_service_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="GitHubDeploymentPolicyPatch")


@_attrs_define
class GitHubDeploymentPolicyPatch:
    """Partial replacement for a project's GitHub deployment policy."""

    root_dir: str | Unset = UNSET
    ignored_paths: list[str] | Unset = UNSET
    preview_enabled: bool | Unset = UNSET
    preview_ttl_hours: int | Unset = UNSET
    preview_service_policy: GitHubDeploymentPolicyPatchPreviewServicePolicy | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        root_dir = self.root_dir

        ignored_paths: list[str] | Unset = UNSET
        if not isinstance(self.ignored_paths, Unset):
            ignored_paths = self.ignored_paths

        preview_enabled = self.preview_enabled

        preview_ttl_hours = self.preview_ttl_hours

        preview_service_policy: str | Unset = UNSET
        if not isinstance(self.preview_service_policy, Unset):
            preview_service_policy = self.preview_service_policy

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if root_dir is not UNSET:
            field_dict["root_dir"] = root_dir
        if ignored_paths is not UNSET:
            field_dict["ignored_paths"] = ignored_paths
        if preview_enabled is not UNSET:
            field_dict["preview_enabled"] = preview_enabled
        if preview_ttl_hours is not UNSET:
            field_dict["preview_ttl_hours"] = preview_ttl_hours
        if preview_service_policy is not UNSET:
            field_dict["preview_service_policy"] = preview_service_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        root_dir = d.pop("root_dir", UNSET)

        ignored_paths = cast(list[str], d.pop("ignored_paths", UNSET))

        preview_enabled = d.pop("preview_enabled", UNSET)

        preview_ttl_hours = d.pop("preview_ttl_hours", UNSET)

        _preview_service_policy = d.pop("preview_service_policy", UNSET)
        preview_service_policy: GitHubDeploymentPolicyPatchPreviewServicePolicy | Unset
        if isinstance(_preview_service_policy, Unset):
            preview_service_policy = UNSET
        else:
            preview_service_policy = check_git_hub_deployment_policy_patch_preview_service_policy(
                _preview_service_policy
            )

        git_hub_deployment_policy_patch = cls(
            root_dir=root_dir,
            ignored_paths=ignored_paths,
            preview_enabled=preview_enabled,
            preview_ttl_hours=preview_ttl_hours,
            preview_service_policy=preview_service_policy,
        )

        return git_hub_deployment_policy_patch
