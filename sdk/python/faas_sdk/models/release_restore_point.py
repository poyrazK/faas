from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ReleaseRestorePoint")


@_attrs_define
class ReleaseRestorePoint:
    """The moment before the earliest crossed release for one managed PostgreSQL database the app's release commands can
    migrate (ADR-1448), with the command that restores it into a new database.

    """

    database_id: UUID
    database_name: str
    point_in_time: datetime.datetime
    command: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        database_name = self.database_name

        point_in_time = self.point_in_time.isoformat()

        command = self.command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "database_name": database_name,
                "point_in_time": point_in_time,
                "command": command,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        database_name = d.pop("database_name")

        point_in_time = datetime.datetime.fromisoformat(d.pop("point_in_time"))

        command = d.pop("command")

        release_restore_point = cls(
            database_id=database_id,
            database_name=database_name,
            point_in_time=point_in_time,
            command=command,
        )

        release_restore_point.additional_properties = d
        return release_restore_point

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
