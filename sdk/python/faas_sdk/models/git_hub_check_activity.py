from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.git_hub_check_activity_status import GitHubCheckActivityStatus, check_git_hub_check_activity_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="GitHubCheckActivity")


@_attrs_define
class GitHubCheckActivity:
    """Redacted Check Run synchronization activity for the bound GitHub app."""

    deployment_id: UUID
    status: GitHubCheckActivityStatus
    updated_at: datetime.datetime
    commit_sha: str | Unset = UNSET
    processed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        status: str = self.status

        updated_at = self.updated_at.isoformat()

        commit_sha = self.commit_sha

        processed_at: None | str | Unset
        if isinstance(self.processed_at, Unset):
            processed_at = UNSET
        elif isinstance(self.processed_at, datetime.datetime):
            processed_at = self.processed_at.isoformat()
        else:
            processed_at = self.processed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "status": status,
                "updated_at": updated_at,
            }
        )
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if processed_at is not UNSET:
            field_dict["processed_at"] = processed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        status = check_git_hub_check_activity_status(d.pop("status"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        commit_sha = d.pop("commit_sha", UNSET)

        def _parse_processed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                processed_at_type_0 = datetime.datetime.fromisoformat(data)

                return processed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        processed_at = _parse_processed_at(d.pop("processed_at", UNSET))

        git_hub_check_activity = cls(
            deployment_id=deployment_id,
            status=status,
            updated_at=updated_at,
            commit_sha=commit_sha,
            processed_at=processed_at,
        )

        git_hub_check_activity.additional_properties = d
        return git_hub_check_activity

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
