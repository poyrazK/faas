from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.git_hub_install_status_health import GitHubInstallStatusHealth, check_git_hub_install_status_health
from ..models.git_hub_install_status_state import GitHubInstallStatusState, check_git_hub_install_status_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.git_hub_install_status_sync_result import GitHubInstallStatusSyncResult


T = TypeVar("T", bound="GitHubInstallStatus")


@_attrs_define
class GitHubInstallStatus:
    """Account-scoped GitHub installation and app binding health. Sealed
    installation credentials are never returned. `state` is
    `not_installed`, `installed`, or `bound`; `health` is
    `not_connected`, `unknown`, `healthy`, or `degraded`.

    """

    state: GitHubInstallStatusState
    health: GitHubInstallStatusHealth
    connected: bool
    last_reconcile_repository_count: int
    last_reconcile_detached_count: int
    installation_id: int | Unset = UNSET
    github_login: str | Unset = UNSET
    default_branch: str | Unset = UNSET
    repo_full_name: str | Unset = UNSET
    production_branch: str | Unset = UNSET
    binding_id: str | Unset = UNSET
    linked_at: datetime.datetime | None | Unset = UNSET
    last_reconciled_at: datetime.datetime | None | Unset = UNSET
    last_reconcile_error: str | Unset = UNSET
    csrf_token: str | Unset = UNSET
    """CSRF token for sync and disconnect mutations."""
    sync_result: GitHubInstallStatusSyncResult | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        health: str = self.health

        connected = self.connected

        last_reconcile_repository_count = self.last_reconcile_repository_count

        last_reconcile_detached_count = self.last_reconcile_detached_count

        installation_id = self.installation_id

        github_login = self.github_login

        default_branch = self.default_branch

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

        binding_id = self.binding_id

        linked_at: None | str | Unset
        if isinstance(self.linked_at, Unset):
            linked_at = UNSET
        elif isinstance(self.linked_at, datetime.datetime):
            linked_at = self.linked_at.isoformat()
        else:
            linked_at = self.linked_at

        last_reconciled_at: None | str | Unset
        if isinstance(self.last_reconciled_at, Unset):
            last_reconciled_at = UNSET
        elif isinstance(self.last_reconciled_at, datetime.datetime):
            last_reconciled_at = self.last_reconciled_at.isoformat()
        else:
            last_reconciled_at = self.last_reconciled_at

        last_reconcile_error = self.last_reconcile_error

        csrf_token = self.csrf_token

        sync_result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.sync_result, Unset):
            sync_result = self.sync_result.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "health": health,
                "connected": connected,
                "last_reconcile_repository_count": last_reconcile_repository_count,
                "last_reconcile_detached_count": last_reconcile_detached_count,
            }
        )
        if installation_id is not UNSET:
            field_dict["installation_id"] = installation_id
        if github_login is not UNSET:
            field_dict["github_login"] = github_login
        if default_branch is not UNSET:
            field_dict["default_branch"] = default_branch
        if repo_full_name is not UNSET:
            field_dict["repo_full_name"] = repo_full_name
        if production_branch is not UNSET:
            field_dict["production_branch"] = production_branch
        if binding_id is not UNSET:
            field_dict["binding_id"] = binding_id
        if linked_at is not UNSET:
            field_dict["linked_at"] = linked_at
        if last_reconciled_at is not UNSET:
            field_dict["last_reconciled_at"] = last_reconciled_at
        if last_reconcile_error is not UNSET:
            field_dict["last_reconcile_error"] = last_reconcile_error
        if csrf_token is not UNSET:
            field_dict["csrf_token"] = csrf_token
        if sync_result is not UNSET:
            field_dict["sync_result"] = sync_result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.git_hub_install_status_sync_result import GitHubInstallStatusSyncResult

        d = dict(src_dict)
        state = check_git_hub_install_status_state(d.pop("state"))

        health = check_git_hub_install_status_health(d.pop("health"))

        connected = d.pop("connected")

        last_reconcile_repository_count = d.pop("last_reconcile_repository_count")

        last_reconcile_detached_count = d.pop("last_reconcile_detached_count")

        installation_id = d.pop("installation_id", UNSET)

        github_login = d.pop("github_login", UNSET)

        default_branch = d.pop("default_branch", UNSET)

        repo_full_name = d.pop("repo_full_name", UNSET)

        production_branch = d.pop("production_branch", UNSET)

        binding_id = d.pop("binding_id", UNSET)

        def _parse_linked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                linked_at_type_0 = datetime.datetime.fromisoformat(data)

                return linked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        linked_at = _parse_linked_at(d.pop("linked_at", UNSET))

        def _parse_last_reconciled_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_reconciled_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_reconciled_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_reconciled_at = _parse_last_reconciled_at(d.pop("last_reconciled_at", UNSET))

        last_reconcile_error = d.pop("last_reconcile_error", UNSET)

        csrf_token = d.pop("csrf_token", UNSET)

        _sync_result = d.pop("sync_result", UNSET)
        sync_result: GitHubInstallStatusSyncResult | Unset
        if isinstance(_sync_result, Unset):
            sync_result = UNSET
        else:
            sync_result = GitHubInstallStatusSyncResult.from_dict(_sync_result)

        git_hub_install_status = cls(
            state=state,
            health=health,
            connected=connected,
            last_reconcile_repository_count=last_reconcile_repository_count,
            last_reconcile_detached_count=last_reconcile_detached_count,
            installation_id=installation_id,
            github_login=github_login,
            default_branch=default_branch,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
            binding_id=binding_id,
            linked_at=linked_at,
            last_reconciled_at=last_reconciled_at,
            last_reconcile_error=last_reconcile_error,
            csrf_token=csrf_token,
            sync_result=sync_result,
        )

        git_hub_install_status.additional_properties = d
        return git_hub_install_status

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
