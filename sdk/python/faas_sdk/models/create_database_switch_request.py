from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateDatabaseSwitchRequest")


@_attrs_define
class CreateDatabaseSwitchRequest:
    """Restore a managed database the app binds and move the app to the copy, keeping its code."""

    database_id: UUID
    """ The managed database the app's bindings point at. """
    name: str
    """ Name of the restored copy; it becomes the app's database. """
    point_in_time: datetime.datetime
    """ A time inside the database's restore range. Writes after it stay only in the source database. """
    scope: str | Unset = UNSET
    """ The environment whose bindings move; empty is the default scope. """

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        name = self.name

        point_in_time = self.point_in_time.isoformat()

        scope = self.scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "database_id": database_id,
                "name": name,
                "point_in_time": point_in_time,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        name = d.pop("name")

        point_in_time = datetime.datetime.fromisoformat(d.pop("point_in_time"))

        scope = d.pop("scope", UNSET)

        create_database_switch_request = cls(
            database_id=database_id,
            name=name,
            point_in_time=point_in_time,
            scope=scope,
        )

        return create_database_switch_request
