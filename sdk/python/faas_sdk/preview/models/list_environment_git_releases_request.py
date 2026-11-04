from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ListEnvironmentGitReleasesRequest")


@_attrs_define
class ListEnvironmentGitReleasesRequest:
    """Validated query inputs for an immutable-target recovery catalog read."""

    target_commit_sha: str
    """Exact target-manifest revision against which recovery eligibility is assessed."""
    before: UUID | Unset = UNSET
    """Scoped anchor release from an earlier catalog response; omitted for the newest page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target_commit_sha = self.target_commit_sha

        before: str | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = str(self.before)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target_commit_sha": target_commit_sha,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target_commit_sha = d.pop("target_commit_sha")

        _before = d.pop("before", UNSET)
        before: UUID | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = UUID(_before)

        list_environment_git_releases_request = cls(
            target_commit_sha=target_commit_sha,
            before=before,
        )

        list_environment_git_releases_request.additional_properties = d
        return list_environment_git_releases_request

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
