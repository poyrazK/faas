from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.postgres_change_feed import PostgresChangeFeed


T = TypeVar("T", bound="PutPostgresChangesRequest")


@_attrs_define
class PutPostgresChangesRequest:
    """Declares the app's Postgres change capture."""

    queue_name: str
    """The app queue binding that receives the changes."""
    feeds: list[PostgresChangeFeed]
    database: str | Unset = UNSET
    """A managed database name or ID. Omit for a database you run."""
    poll_interval_seconds: int | Unset = UNSET
    """How often the outbox is read; default 5. 300 or more lets an idle managed database suspend."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queue_name = self.queue_name

        feeds = []
        for feeds_item_data in self.feeds:
            feeds_item = feeds_item_data.to_dict()
            feeds.append(feeds_item)

        database = self.database

        poll_interval_seconds = self.poll_interval_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "queue_name": queue_name,
                "feeds": feeds,
            }
        )
        if database is not UNSET:
            field_dict["database"] = database
        if poll_interval_seconds is not UNSET:
            field_dict["poll_interval_seconds"] = poll_interval_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.postgres_change_feed import PostgresChangeFeed

        d = dict(src_dict)
        queue_name = d.pop("queue_name")

        feeds = []
        _feeds = d.pop("feeds")
        for feeds_item_data in _feeds:
            feeds_item = PostgresChangeFeed.from_dict(feeds_item_data)

            feeds.append(feeds_item)

        database = d.pop("database", UNSET)

        poll_interval_seconds = d.pop("poll_interval_seconds", UNSET)

        put_postgres_changes_request = cls(
            queue_name=queue_name,
            feeds=feeds,
            database=database,
            poll_interval_seconds=poll_interval_seconds,
        )

        put_postgres_changes_request.additional_properties = d
        return put_postgres_changes_request

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
