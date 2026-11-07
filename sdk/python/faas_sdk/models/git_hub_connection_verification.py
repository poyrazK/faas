from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.git_hub_connection_verification_permissions import GitHubConnectionVerificationPermissions


T = TypeVar("T", bound="GitHubConnectionVerification")


@_attrs_define
class GitHubConnectionVerification:
    """Live GitHub installation permissions, repository selection and verification time."""

    account_type: str
    repository_selection: str
    permissions: GitHubConnectionVerificationPermissions
    verified_at: datetime.datetime
    repository_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_type = self.account_type

        repository_selection = self.repository_selection

        permissions = self.permissions.to_dict()

        verified_at = self.verified_at.isoformat()

        repository_count = self.repository_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_type": account_type,
                "repository_selection": repository_selection,
                "permissions": permissions,
                "verified_at": verified_at,
                "repository_count": repository_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.git_hub_connection_verification_permissions import GitHubConnectionVerificationPermissions

        d = dict(src_dict)
        account_type = d.pop("account_type")

        repository_selection = d.pop("repository_selection")

        permissions = GitHubConnectionVerificationPermissions.from_dict(d.pop("permissions"))

        verified_at = datetime.datetime.fromisoformat(d.pop("verified_at"))

        repository_count = d.pop("repository_count")

        git_hub_connection_verification = cls(
            account_type=account_type,
            repository_selection=repository_selection,
            permissions=permissions,
            verified_at=verified_at,
            repository_count=repository_count,
        )

        git_hub_connection_verification.additional_properties = d
        return git_hub_connection_verification

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
