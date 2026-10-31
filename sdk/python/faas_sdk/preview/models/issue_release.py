from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueRelease")


@_attrs_define
class IssueRelease:
    """Immutable deployment metadata and aggregate observations for one issue."""

    deployment_id: UUID
    event_count: int
    first_seen_at: datetime.datetime
    last_seen_at: datetime.datetime
    commit_sha: str | Unset = UNSET
    image_digest: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        event_count = self.event_count

        first_seen_at = self.first_seen_at.isoformat()

        last_seen_at = self.last_seen_at.isoformat()

        commit_sha = self.commit_sha

        image_digest = self.image_digest

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "event_count": event_count,
                "first_seen_at": first_seen_at,
                "last_seen_at": last_seen_at,
            }
        )
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        event_count = d.pop("event_count")

        first_seen_at = datetime.datetime.fromisoformat(d.pop("first_seen_at"))

        last_seen_at = datetime.datetime.fromisoformat(d.pop("last_seen_at"))

        commit_sha = d.pop("commit_sha", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        issue_release = cls(
            deployment_id=deployment_id,
            event_count=event_count,
            first_seen_at=first_seen_at,
            last_seen_at=last_seen_at,
            commit_sha=commit_sha,
            image_digest=image_digest,
        )

        issue_release.additional_properties = d
        return issue_release

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
