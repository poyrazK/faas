from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="GitHubInstallMutationRequest")


@_attrs_define
class GitHubInstallMutationRequest:
    """Double-submit CSRF envelope for customer-facing GitHub connection
    mutations. The token is returned by GET /v1/apps/{slug}/install or
    GET /v1/apps/{slug}/install/bind and must match the named
    `faas_csrf_github_install` cookie.

    """

    csrf_token: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        csrf_token = self.csrf_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "csrf_token": csrf_token,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        csrf_token = d.pop("csrf_token")

        git_hub_install_mutation_request = cls(
            csrf_token=csrf_token,
        )

        git_hub_install_mutation_request.additional_properties = d
        return git_hub_install_mutation_request

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
