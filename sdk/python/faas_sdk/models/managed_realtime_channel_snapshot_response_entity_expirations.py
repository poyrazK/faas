from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedRealtimeChannelSnapshotResponseEntityExpirations")


@_attrs_define
class ManagedRealtimeChannelSnapshotResponseEntityExpirations:
    """Scheduled entity deadlines; cleanup is asynchronous."""

    additional_properties: dict[str, datetime.datetime] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.isoformat()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        managed_realtime_channel_snapshot_response_entity_expirations = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = datetime.datetime.fromisoformat(prop_dict)

            additional_properties[prop_name] = additional_property

        managed_realtime_channel_snapshot_response_entity_expirations.additional_properties = additional_properties
        return managed_realtime_channel_snapshot_response_entity_expirations

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> datetime.datetime:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: datetime.datetime) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
