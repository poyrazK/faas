from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.git_hub_account_connection_health import (
    GitHubAccountConnectionHealth,
    check_git_hub_account_connection_health,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.git_hub_connection_verification import GitHubConnectionVerification


T = TypeVar("T", bound="GitHubAccountConnection")


@_attrs_define
class GitHubAccountConnection:
    """Credential-free health and access summary for one account-owned deployment authorization."""

    installation_id: int
    github_login: str
    health: GitHubAccountConnectionHealth
    repository_count: int
    access: GitHubConnectionVerification | Unset = UNSET
    """Live GitHub installation permissions, repository selection and verification time."""
    last_verified_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        installation_id = self.installation_id

        github_login = self.github_login

        health: str = self.health

        repository_count = self.repository_count

        access: dict[str, Any] | Unset = UNSET
        if not isinstance(self.access, Unset):
            access = self.access.to_dict()

        last_verified_at: str | Unset = UNSET
        if not isinstance(self.last_verified_at, Unset):
            last_verified_at = self.last_verified_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "installation_id": installation_id,
                "github_login": github_login,
                "health": health,
                "repository_count": repository_count,
            }
        )
        if access is not UNSET:
            field_dict["access"] = access
        if last_verified_at is not UNSET:
            field_dict["last_verified_at"] = last_verified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.git_hub_connection_verification import GitHubConnectionVerification

        d = dict(src_dict)
        installation_id = d.pop("installation_id")

        github_login = d.pop("github_login")

        health = check_git_hub_account_connection_health(d.pop("health"))

        repository_count = d.pop("repository_count")

        _access = d.pop("access", UNSET)
        access: GitHubConnectionVerification | Unset
        if isinstance(_access, Unset):
            access = UNSET
        else:
            access = GitHubConnectionVerification.from_dict(_access)

        _last_verified_at = d.pop("last_verified_at", UNSET)
        last_verified_at: datetime.datetime | Unset
        if isinstance(_last_verified_at, Unset):
            last_verified_at = UNSET
        else:
            last_verified_at = datetime.datetime.fromisoformat(_last_verified_at)

        git_hub_account_connection = cls(
            installation_id=installation_id,
            github_login=github_login,
            health=health,
            repository_count=repository_count,
            access=access,
            last_verified_at=last_verified_at,
        )

        git_hub_account_connection.additional_properties = d
        return git_hub_account_connection

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
