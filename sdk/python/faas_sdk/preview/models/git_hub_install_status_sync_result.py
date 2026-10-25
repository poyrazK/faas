from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="GitHubInstallStatusSyncResult")


@_attrs_define
class GitHubInstallStatusSyncResult:
    detached: bool | Unset = UNSET
    remote_repository_count: int | Unset = UNSET
    synced_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        detached = self.detached

        remote_repository_count = self.remote_repository_count

        synced_at: str | Unset = UNSET
        if not isinstance(self.synced_at, Unset):
            synced_at = self.synced_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if detached is not UNSET:
            field_dict["detached"] = detached
        if remote_repository_count is not UNSET:
            field_dict["remote_repository_count"] = remote_repository_count
        if synced_at is not UNSET:
            field_dict["synced_at"] = synced_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        detached = d.pop("detached", UNSET)

        remote_repository_count = d.pop("remote_repository_count", UNSET)

        _synced_at = d.pop("synced_at", UNSET)
        synced_at: datetime.datetime | Unset
        if isinstance(_synced_at, Unset):
            synced_at = UNSET
        else:
            synced_at = datetime.datetime.fromisoformat(_synced_at)

        git_hub_install_status_sync_result = cls(
            detached=detached,
            remote_repository_count=remote_repository_count,
            synced_at=synced_at,
        )

        git_hub_install_status_sync_result.additional_properties = d
        return git_hub_install_status_sync_result

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
