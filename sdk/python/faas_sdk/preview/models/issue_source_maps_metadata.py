from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueSourceMapsMetadata")


@_attrs_define
class IssueSourceMapsMetadata:
    """Read-only publication status, manifest digest and retained map count for one deployment."""

    deployment_id: UUID
    manifest_sha256: str
    map_count: int
    ready: bool
    created_at: datetime.datetime
    retired_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        manifest_sha256 = self.manifest_sha256

        map_count = self.map_count

        ready = self.ready

        created_at = self.created_at.isoformat()

        retired_at: str | Unset = UNSET
        if not isinstance(self.retired_at, Unset):
            retired_at = self.retired_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "manifest_sha256": manifest_sha256,
                "map_count": map_count,
                "ready": ready,
                "created_at": created_at,
            }
        )
        if retired_at is not UNSET:
            field_dict["retired_at"] = retired_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        manifest_sha256 = d.pop("manifest_sha256")

        map_count = d.pop("map_count")

        ready = d.pop("ready")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _retired_at = d.pop("retired_at", UNSET)
        retired_at: datetime.datetime | Unset
        if isinstance(_retired_at, Unset):
            retired_at = UNSET
        else:
            retired_at = datetime.datetime.fromisoformat(_retired_at)

        issue_source_maps_metadata = cls(
            deployment_id=deployment_id,
            manifest_sha256=manifest_sha256,
            map_count=map_count,
            ready=ready,
            created_at=created_at,
            retired_at=retired_at,
        )

        issue_source_maps_metadata.additional_properties = d
        return issue_source_maps_metadata

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
