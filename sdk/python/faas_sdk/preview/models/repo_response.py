from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RepoResponse")


@_attrs_define
class RepoResponse:
    """Repo visible to the user's GitHub App installation, as
    returned by githubd's `/user/installations/{id}/repositories`.
    Carries only the fields the dashboard bind picker renders —
    no nested owner object (the install URL already disambiguates).

    """

    id: int
    full_name: str
    default_branch: str
    private: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        full_name = self.full_name

        default_branch = self.default_branch

        private = self.private

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "full_name": full_name,
                "default_branch": default_branch,
                "private": private,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        full_name = d.pop("full_name")

        default_branch = d.pop("default_branch")

        private = d.pop("private")

        repo_response = cls(
            id=id,
            full_name=full_name,
            default_branch=default_branch,
            private=private,
        )

        repo_response.additional_properties = d
        return repo_response

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
