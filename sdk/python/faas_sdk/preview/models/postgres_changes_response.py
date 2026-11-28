from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.postgres_change_feed import PostgresChangeFeed


T = TypeVar("T", bound="PostgresChangesResponse")


@_attrs_define
class PostgresChangesResponse:
    """The app's Postgres change capture and its relay health."""

    source_id: UUID
    queue_name: str
    managed: bool
    poll_interval_seconds: int
    feeds: list[PostgresChangeFeed]
    enabled: bool
    connected: bool
    """Whether Gregale holds the relay login."""
    relay_role: str
    """The least-privilege login the install creates for delivery."""
    relay_status: str
    database: str | Unset = UNSET
    """The managed database name, when managed."""
    last_checked_at: datetime.datetime | Unset = UNSET
    pending_events: int | Unset = UNSET
    blocked_events: int | Unset = UNSET
    oldest_pending_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_id = str(self.source_id)

        queue_name = self.queue_name

        managed = self.managed

        poll_interval_seconds = self.poll_interval_seconds

        feeds = []
        for feeds_item_data in self.feeds:
            feeds_item = feeds_item_data.to_dict()
            feeds.append(feeds_item)

        enabled = self.enabled

        connected = self.connected

        relay_role = self.relay_role

        relay_status = self.relay_status

        database = self.database

        last_checked_at: str | Unset = UNSET
        if not isinstance(self.last_checked_at, Unset):
            last_checked_at = self.last_checked_at.isoformat()

        pending_events = self.pending_events

        blocked_events = self.blocked_events

        oldest_pending_at: str | Unset = UNSET
        if not isinstance(self.oldest_pending_at, Unset):
            oldest_pending_at = self.oldest_pending_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_id": source_id,
                "queue_name": queue_name,
                "managed": managed,
                "poll_interval_seconds": poll_interval_seconds,
                "feeds": feeds,
                "enabled": enabled,
                "connected": connected,
                "relay_role": relay_role,
                "relay_status": relay_status,
            }
        )
        if database is not UNSET:
            field_dict["database"] = database
        if last_checked_at is not UNSET:
            field_dict["last_checked_at"] = last_checked_at
        if pending_events is not UNSET:
            field_dict["pending_events"] = pending_events
        if blocked_events is not UNSET:
            field_dict["blocked_events"] = blocked_events
        if oldest_pending_at is not UNSET:
            field_dict["oldest_pending_at"] = oldest_pending_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.postgres_change_feed import PostgresChangeFeed

        d = dict(src_dict)
        source_id = UUID(d.pop("source_id"))

        queue_name = d.pop("queue_name")

        managed = d.pop("managed")

        poll_interval_seconds = d.pop("poll_interval_seconds")

        feeds = []
        _feeds = d.pop("feeds")
        for feeds_item_data in _feeds:
            feeds_item = PostgresChangeFeed.from_dict(feeds_item_data)

            feeds.append(feeds_item)

        enabled = d.pop("enabled")

        connected = d.pop("connected")

        relay_role = d.pop("relay_role")

        relay_status = d.pop("relay_status")

        database = d.pop("database", UNSET)

        _last_checked_at = d.pop("last_checked_at", UNSET)
        last_checked_at: datetime.datetime | Unset
        if isinstance(_last_checked_at, Unset):
            last_checked_at = UNSET
        else:
            last_checked_at = datetime.datetime.fromisoformat(_last_checked_at)

        pending_events = d.pop("pending_events", UNSET)

        blocked_events = d.pop("blocked_events", UNSET)

        _oldest_pending_at = d.pop("oldest_pending_at", UNSET)
        oldest_pending_at: datetime.datetime | Unset
        if isinstance(_oldest_pending_at, Unset):
            oldest_pending_at = UNSET
        else:
            oldest_pending_at = datetime.datetime.fromisoformat(_oldest_pending_at)

        postgres_changes_response = cls(
            source_id=source_id,
            queue_name=queue_name,
            managed=managed,
            poll_interval_seconds=poll_interval_seconds,
            feeds=feeds,
            enabled=enabled,
            connected=connected,
            relay_role=relay_role,
            relay_status=relay_status,
            database=database,
            last_checked_at=last_checked_at,
            pending_events=pending_events,
            blocked_events=blocked_events,
            oldest_pending_at=oldest_pending_at,
        )

        postgres_changes_response.additional_properties = d
        return postgres_changes_response

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
