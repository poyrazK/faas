from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_postgres_query_error import ManagedPostgresQueryError


T = TypeVar("T", bound="ManagedPostgresErrorHistory")


@_attrs_define
class ManagedPostgresErrorHistory:
    """Pooled error collection settings and retained events matching the filter."""

    database_id: UUID
    enabled: bool
    since: datetime.datetime
    events: list[ManagedPostgresQueryError]
    has_more: bool
    """More retained events match the filter than the requested limit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        enabled = self.enabled

        since = self.since.isoformat()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        has_more = self.has_more

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "enabled": enabled,
                "since": since,
                "events": events,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_query_error import ManagedPostgresQueryError

        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        enabled = d.pop("enabled")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = ManagedPostgresQueryError.from_dict(events_item_data)

            events.append(events_item)

        has_more = d.pop("has_more")

        managed_postgres_error_history = cls(
            database_id=database_id,
            enabled=enabled,
            since=since,
            events=events,
            has_more=has_more,
        )

        managed_postgres_error_history.additional_properties = d
        return managed_postgres_error_history

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
