from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimePublisherCredential")


@_attrs_define
class RealtimePublisherCredential:
    """Realtime Publisher Credential."""

    name: str
    channels: list[str]
    event_types: list[str]
    id: str
    endpoint_id: str
    generation: str
    issued_at: datetime.datetime
    expires_at: datetime.datetime
    revoked_at: datetime.datetime | None
    accepted_count: str
    replay_count: str
    last_used_at: datetime.datetime | None
    last_channel: None | str
    last_sequence: None | str
    publishes_per_minute: int = 60
    max_payload_bytes: int = 4096
    max_message_ttl_seconds: int | Unset = 0
    """Immutable message TTL cap; zero is uncapped. Rotation preserves this policy."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        channels = self.channels

        event_types = self.event_types

        publishes_per_minute = self.publishes_per_minute

        max_payload_bytes = self.max_payload_bytes

        id = self.id

        endpoint_id = self.endpoint_id

        generation = self.generation

        issued_at = self.issued_at.isoformat()

        expires_at = self.expires_at.isoformat()

        revoked_at: None | str
        if isinstance(self.revoked_at, datetime.datetime):
            revoked_at = self.revoked_at.isoformat()
        else:
            revoked_at = self.revoked_at

        accepted_count = self.accepted_count

        replay_count = self.replay_count

        last_used_at: None | str
        if isinstance(self.last_used_at, datetime.datetime):
            last_used_at = self.last_used_at.isoformat()
        else:
            last_used_at = self.last_used_at

        last_channel: None | str
        last_channel = self.last_channel

        last_sequence: None | str
        last_sequence = self.last_sequence

        max_message_ttl_seconds = self.max_message_ttl_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "channels": channels,
                "event_types": event_types,
                "publishes_per_minute": publishes_per_minute,
                "max_payload_bytes": max_payload_bytes,
                "id": id,
                "endpoint_id": endpoint_id,
                "generation": generation,
                "issued_at": issued_at,
                "expires_at": expires_at,
                "revoked_at": revoked_at,
                "accepted_count": accepted_count,
                "replay_count": replay_count,
                "last_used_at": last_used_at,
                "last_channel": last_channel,
                "last_sequence": last_sequence,
            }
        )
        if max_message_ttl_seconds is not UNSET:
            field_dict["max_message_ttl_seconds"] = max_message_ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        channels = cast(list[str], d.pop("channels"))

        event_types = cast(list[str], d.pop("event_types"))

        publishes_per_minute = d.pop("publishes_per_minute")

        max_payload_bytes = d.pop("max_payload_bytes")

        id = d.pop("id")

        endpoint_id = d.pop("endpoint_id")

        generation = d.pop("generation")

        issued_at = datetime.datetime.fromisoformat(d.pop("issued_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        def _parse_revoked_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                revoked_at_type_0 = datetime.datetime.fromisoformat(data)

                return revoked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        revoked_at = _parse_revoked_at(d.pop("revoked_at"))

        accepted_count = d.pop("accepted_count")

        replay_count = d.pop("replay_count")

        def _parse_last_used_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_used_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_used_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        last_used_at = _parse_last_used_at(d.pop("last_used_at"))

        def _parse_last_channel(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        last_channel = _parse_last_channel(d.pop("last_channel"))

        def _parse_last_sequence(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        last_sequence = _parse_last_sequence(d.pop("last_sequence"))

        max_message_ttl_seconds = d.pop("max_message_ttl_seconds", UNSET)

        realtime_publisher_credential = cls(
            name=name,
            channels=channels,
            event_types=event_types,
            publishes_per_minute=publishes_per_minute,
            max_payload_bytes=max_payload_bytes,
            id=id,
            endpoint_id=endpoint_id,
            generation=generation,
            issued_at=issued_at,
            expires_at=expires_at,
            revoked_at=revoked_at,
            accepted_count=accepted_count,
            replay_count=replay_count,
            last_used_at=last_used_at,
            last_channel=last_channel,
            last_sequence=last_sequence,
            max_message_ttl_seconds=max_message_ttl_seconds,
        )

        realtime_publisher_credential.additional_properties = d
        return realtime_publisher_credential

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
