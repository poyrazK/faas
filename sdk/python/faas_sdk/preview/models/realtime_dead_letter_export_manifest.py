from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_dead_letter_export_manifest_format_version import (
    RealtimeDeadLetterExportManifestFormatVersion,
    check_realtime_dead_letter_export_manifest_format_version,
)
from ..models.realtime_dead_letter_export_manifest_type import (
    RealtimeDeadLetterExportManifestType,
    check_realtime_dead_letter_export_manifest_type,
)

T = TypeVar("T", bound="RealtimeDeadLetterExportManifest")


@_attrs_define
class RealtimeDeadLetterExportManifest:
    """Realtime Dead Letter Export Manifest."""

    type_: RealtimeDeadLetterExportManifestType
    format_version: RealtimeDeadLetterExportManifestFormatVersion
    group_id: UUID
    group: str
    channel: str
    exported_at: datetime.datetime
    snapshot_at: datetime.datetime
    after_sequence: str
    through_sequence: str
    failure_codes: list[str]
    count: int
    has_more: bool
    next_after_sequence: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        format_version: int = self.format_version

        group_id = str(self.group_id)

        group = self.group

        channel = self.channel

        exported_at = self.exported_at.isoformat()

        snapshot_at = self.snapshot_at.isoformat()

        after_sequence = self.after_sequence

        through_sequence = self.through_sequence

        failure_codes = self.failure_codes

        count = self.count

        has_more = self.has_more

        next_after_sequence = self.next_after_sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "format_version": format_version,
                "group_id": group_id,
                "group": group,
                "channel": channel,
                "exported_at": exported_at,
                "snapshot_at": snapshot_at,
                "after_sequence": after_sequence,
                "through_sequence": through_sequence,
                "failure_codes": failure_codes,
                "count": count,
                "has_more": has_more,
                "next_after_sequence": next_after_sequence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_realtime_dead_letter_export_manifest_type(d.pop("type"))

        format_version = check_realtime_dead_letter_export_manifest_format_version(d.pop("format_version"))

        group_id = UUID(d.pop("group_id"))

        group = d.pop("group")

        channel = d.pop("channel")

        exported_at = datetime.datetime.fromisoformat(d.pop("exported_at"))

        snapshot_at = datetime.datetime.fromisoformat(d.pop("snapshot_at"))

        after_sequence = d.pop("after_sequence")

        through_sequence = d.pop("through_sequence")

        failure_codes = cast(list[str], d.pop("failure_codes"))

        count = d.pop("count")

        has_more = d.pop("has_more")

        next_after_sequence = d.pop("next_after_sequence")

        realtime_dead_letter_export_manifest = cls(
            type_=type_,
            format_version=format_version,
            group_id=group_id,
            group=group,
            channel=channel,
            exported_at=exported_at,
            snapshot_at=snapshot_at,
            after_sequence=after_sequence,
            through_sequence=through_sequence,
            failure_codes=failure_codes,
            count=count,
            has_more=has_more,
            next_after_sequence=next_after_sequence,
        )

        realtime_dead_letter_export_manifest.additional_properties = d
        return realtime_dead_letter_export_manifest

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
