from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.rebind_environment_git_source_request_approval_policy import (
    RebindEnvironmentGitSourceRequestApprovalPolicy,
    check_rebind_environment_git_source_request_approval_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RebindEnvironmentGitSourceRequest")


@_attrs_define
class RebindEnvironmentGitSourceRequest:
    """Replace a Git binding with a fresh report-only source that requires a new approval."""

    expected_generation: int
    manifest_path: str
    ref: str | Unset = UNSET
    approval_policy: RebindEnvironmentGitSourceRequestApprovalPolicy | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_generation = self.expected_generation

        manifest_path = self.manifest_path

        ref = self.ref

        approval_policy: str | Unset = UNSET
        if not isinstance(self.approval_policy, Unset):
            approval_policy = self.approval_policy

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_generation": expected_generation,
                "manifest_path": manifest_path,
            }
        )
        if ref is not UNSET:
            field_dict["ref"] = ref
        if approval_policy is not UNSET:
            field_dict["approval_policy"] = approval_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_generation = d.pop("expected_generation")

        manifest_path = d.pop("manifest_path")

        ref = d.pop("ref", UNSET)

        _approval_policy = d.pop("approval_policy", UNSET)
        approval_policy: RebindEnvironmentGitSourceRequestApprovalPolicy | Unset
        if isinstance(_approval_policy, Unset):
            approval_policy = UNSET
        else:
            approval_policy = check_rebind_environment_git_source_request_approval_policy(_approval_policy)

        rebind_environment_git_source_request = cls(
            expected_generation=expected_generation,
            manifest_path=manifest_path,
            ref=ref,
            approval_policy=approval_policy,
        )

        return rebind_environment_git_source_request
