from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_channel_snapshot_response_entity_expirations import (
        ManagedRealtimeChannelSnapshotResponseEntityExpirations,
    )
    from ..models.managed_realtime_channel_snapshot_response_entity_versions import (
        ManagedRealtimeChannelSnapshotResponseEntityVersions,
    )


T = TypeVar("T", bound="ManagedRealtimeChannelSnapshotResponse")


@_attrs_define
class ManagedRealtimeChannelSnapshotResponse:
    """Channel state snapshot with sequence and entity expiration metadata."""

    channel: str
    sequence: int
    resume_after_sequence: int
    data_base64: str
    binary: bool
    updated_at: datetime.datetime
    expires_at: datetime.datetime
    entity_expirations: ManagedRealtimeChannelSnapshotResponseEntityExpirations | Unset = UNSET
    """Entity cleanup deadlines carried by this channel snapshot; cleanup runs asynchronously."""
    entity_versions: ManagedRealtimeChannelSnapshotResponseEntityVersions | Unset = UNSET
    """Entity versions, including deletion tombstones; reducer snapshots only."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        channel = self.channel

        sequence = self.sequence

        resume_after_sequence = self.resume_after_sequence

        data_base64 = self.data_base64

        binary = self.binary

        updated_at = self.updated_at.isoformat()

        expires_at = self.expires_at.isoformat()

        entity_expirations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.entity_expirations, Unset):
            entity_expirations = self.entity_expirations.to_dict()

        entity_versions: dict[str, Any] | Unset = UNSET
        if not isinstance(self.entity_versions, Unset):
            entity_versions = self.entity_versions.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "channel": channel,
                "sequence": sequence,
                "resume_after_sequence": resume_after_sequence,
                "data_base64": data_base64,
                "binary": binary,
                "updated_at": updated_at,
                "expires_at": expires_at,
            }
        )
        if entity_expirations is not UNSET:
            field_dict["entity_expirations"] = entity_expirations
        if entity_versions is not UNSET:
            field_dict["entity_versions"] = entity_versions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_channel_snapshot_response_entity_expirations import (
            ManagedRealtimeChannelSnapshotResponseEntityExpirations,
        )
        from ..models.managed_realtime_channel_snapshot_response_entity_versions import (
            ManagedRealtimeChannelSnapshotResponseEntityVersions,
        )

        d = dict(src_dict)
        channel = d.pop("channel")

        sequence = d.pop("sequence")

        resume_after_sequence = d.pop("resume_after_sequence")

        data_base64 = d.pop("data_base64")

        binary = d.pop("binary")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _entity_expirations = d.pop("entity_expirations", UNSET)
        entity_expirations: ManagedRealtimeChannelSnapshotResponseEntityExpirations | Unset
        if isinstance(_entity_expirations, Unset):
            entity_expirations = UNSET
        else:
            entity_expirations = ManagedRealtimeChannelSnapshotResponseEntityExpirations.from_dict(_entity_expirations)

        _entity_versions = d.pop("entity_versions", UNSET)
        entity_versions: ManagedRealtimeChannelSnapshotResponseEntityVersions | Unset
        if isinstance(_entity_versions, Unset):
            entity_versions = UNSET
        else:
            entity_versions = ManagedRealtimeChannelSnapshotResponseEntityVersions.from_dict(_entity_versions)

        managed_realtime_channel_snapshot_response = cls(
            channel=channel,
            sequence=sequence,
            resume_after_sequence=resume_after_sequence,
            data_base64=data_base64,
            binary=binary,
            updated_at=updated_at,
            expires_at=expires_at,
            entity_expirations=entity_expirations,
            entity_versions=entity_versions,
        )

        managed_realtime_channel_snapshot_response.additional_properties = d
        return managed_realtime_channel_snapshot_response

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
