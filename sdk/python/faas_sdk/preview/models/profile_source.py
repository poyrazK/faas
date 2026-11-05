from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProfileSource")


@_attrs_define
class ProfileSource:
    """Deployment-recorded GitHub provenance. Links use a full immutable commit SHA; uploaded or generated source is not
    verified against that commit. Unavailable provenance is explicit.

    """

    available: bool
    repository: str | Unset = UNSET
    commit_sha: str | Unset = UNSET
    commit_url: str | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        repository = self.repository

        commit_sha = self.commit_sha

        commit_url = self.commit_url

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
            }
        )
        if repository is not UNSET:
            field_dict["repository"] = repository
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if commit_url is not UNSET:
            field_dict["commit_url"] = commit_url
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        available = d.pop("available")

        repository = d.pop("repository", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        commit_url = d.pop("commit_url", UNSET)

        reason = d.pop("reason", UNSET)

        profile_source = cls(
            available=available,
            repository=repository,
            commit_sha=commit_sha,
            commit_url=commit_url,
            reason=reason,
        )

        profile_source.additional_properties = d
        return profile_source

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
