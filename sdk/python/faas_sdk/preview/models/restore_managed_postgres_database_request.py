from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RestoreManagedPostgresDatabaseRequest")


@_attrs_define
class RestoreManagedPostgresDatabaseRequest:
    """Point-in-time restore request that creates a new database."""

    name: str
    point_in_time: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        point_in_time = self.point_in_time.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "point_in_time": point_in_time,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        point_in_time = datetime.datetime.fromisoformat(d.pop("point_in_time"))

        restore_managed_postgres_database_request = cls(
            name=name,
            point_in_time=point_in_time,
        )

        return restore_managed_postgres_database_request
