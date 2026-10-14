from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_environment_git_source_request_approval_policy import (
    CreateEnvironmentGitSourceRequestApprovalPolicy,
    check_create_environment_git_source_request_approval_policy,
)
from ..models.create_environment_git_source_request_mode import (
    CreateEnvironmentGitSourceRequestMode,
    check_create_environment_git_source_request_mode,
)
from ..models.create_environment_git_source_request_provider import (
    CreateEnvironmentGitSourceRequestProvider,
    check_create_environment_git_source_request_provider,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateEnvironmentGitSourceRequest")


@_attrs_define
class CreateEnvironmentGitSourceRequest:
    """Select a Git definition. GitHub uses the project's bound repository; GitLab.com requires an account OAuth
    connection, a repository path and an explicit ref. Repository identity is verified by the server. GitLab.com
    currently supports report mode, manual approval and prune=false.

    """

    manifest_path: str
    provider: CreateEnvironmentGitSourceRequestProvider | Unset = "github"
    repository: str | Unset = UNSET
    """Canonical GitLab.com group/project path, including subgroups when present."""
    ref: str | Unset = UNSET
    """Git ref selecting candidates; GitHub defaults to the project production branch."""
    mode: CreateEnvironmentGitSourceRequestMode | Unset = "report"
    approval_policy: CreateEnvironmentGitSourceRequestApprovalPolicy | Unset = "manual"
    prune: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        manifest_path = self.manifest_path

        provider: str | Unset = UNSET
        if not isinstance(self.provider, Unset):
            provider = self.provider

        repository = self.repository

        ref = self.ref

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        approval_policy: str | Unset = UNSET
        if not isinstance(self.approval_policy, Unset):
            approval_policy = self.approval_policy

        prune = self.prune

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "manifest_path": manifest_path,
            }
        )
        if provider is not UNSET:
            field_dict["provider"] = provider
        if repository is not UNSET:
            field_dict["repository"] = repository
        if ref is not UNSET:
            field_dict["ref"] = ref
        if mode is not UNSET:
            field_dict["mode"] = mode
        if approval_policy is not UNSET:
            field_dict["approval_policy"] = approval_policy
        if prune is not UNSET:
            field_dict["prune"] = prune

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        manifest_path = d.pop("manifest_path")

        _provider = d.pop("provider", UNSET)
        provider: CreateEnvironmentGitSourceRequestProvider | Unset
        if isinstance(_provider, Unset):
            provider = UNSET
        else:
            provider = check_create_environment_git_source_request_provider(_provider)

        repository = d.pop("repository", UNSET)

        ref = d.pop("ref", UNSET)

        _mode = d.pop("mode", UNSET)
        mode: CreateEnvironmentGitSourceRequestMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_create_environment_git_source_request_mode(_mode)

        _approval_policy = d.pop("approval_policy", UNSET)
        approval_policy: CreateEnvironmentGitSourceRequestApprovalPolicy | Unset
        if isinstance(_approval_policy, Unset):
            approval_policy = UNSET
        else:
            approval_policy = check_create_environment_git_source_request_approval_policy(_approval_policy)

        prune = d.pop("prune", UNSET)

        create_environment_git_source_request = cls(
            manifest_path=manifest_path,
            provider=provider,
            repository=repository,
            ref=ref,
            mode=mode,
            approval_policy=approval_policy,
            prune=prune,
        )

        return create_environment_git_source_request
