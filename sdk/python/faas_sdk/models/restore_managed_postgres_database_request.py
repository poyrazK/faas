from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RestoreManagedPostgresDatabaseRequest")


@_attrs_define
class RestoreManagedPostgresDatabaseRequest:
    """Point-in-time restore request that creates a new database. A restore is a copy-on-write branch of the source where
    the backend supports it.

    """

    name: str
    point_in_time: datetime.datetime | Unset = UNSET
    """A time inside the source's restore_range. Omit it to restore from the latest point, a couple of seconds
    before the request."""
    ttl_seconds: int | Unset = UNSET
    """Purge the new database this many seconds after it is created. Its bindings are revoked first; the source is
    never touched. The database shows expires_at."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        point_in_time: str | Unset = UNSET
        if not isinstance(self.point_in_time, Unset):
            point_in_time = self.point_in_time.isoformat()

        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
            }
        )
        if point_in_time is not UNSET:
            field_dict["point_in_time"] = point_in_time
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        _point_in_time = d.pop("point_in_time", UNSET)
        point_in_time: datetime.datetime | Unset
        if isinstance(_point_in_time, Unset):
            point_in_time = UNSET
        else:
            point_in_time = datetime.datetime.fromisoformat(_point_in_time)

        ttl_seconds = d.pop("ttl_seconds", UNSET)

        restore_managed_postgres_database_request = cls(
            name=name,
            point_in_time=point_in_time,
            ttl_seconds=ttl_seconds,
        )

        return restore_managed_postgres_database_request
