from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_git_source_spec_approval_policy import (
    EnvironmentGitSourceSpecApprovalPolicy,
    check_environment_git_source_spec_approval_policy,
)
from ..models.environment_git_source_spec_mode import (
    EnvironmentGitSourceSpecMode,
    check_environment_git_source_spec_mode,
)

T = TypeVar("T", bound="EnvironmentGitSourceSpec")


@_attrs_define
class EnvironmentGitSourceSpec:
    """Pinned repository identity and source-of-truth controls for one environment."""

    repository_id: int
    installation_id: int
    repository: str
    ref: str
    manifest_path: str
    mode: EnvironmentGitSourceSpecMode
    approval_policy: EnvironmentGitSourceSpecApprovalPolicy
    prune: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        repository_id = self.repository_id

        installation_id = self.installation_id

        repository = self.repository

        ref = self.ref

        manifest_path = self.manifest_path

        mode: str = self.mode

        approval_policy: str = self.approval_policy

        prune = self.prune

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "repository_id": repository_id,
                "installation_id": installation_id,
                "repository": repository,
                "ref": ref,
                "manifest_path": manifest_path,
                "mode": mode,
                "approval_policy": approval_policy,
                "prune": prune,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        repository_id = d.pop("repository_id")

        installation_id = d.pop("installation_id")

        repository = d.pop("repository")

        ref = d.pop("ref")

        manifest_path = d.pop("manifest_path")

        mode = check_environment_git_source_spec_mode(d.pop("mode"))

        approval_policy = check_environment_git_source_spec_approval_policy(d.pop("approval_policy"))

        prune = d.pop("prune")

        environment_git_source_spec = cls(
            repository_id=repository_id,
            installation_id=installation_id,
            repository=repository,
            ref=ref,
            manifest_path=manifest_path,
            mode=mode,
            approval_policy=approval_policy,
            prune=prune,
        )

        environment_git_source_spec.additional_properties = d
        return environment_git_source_spec

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
