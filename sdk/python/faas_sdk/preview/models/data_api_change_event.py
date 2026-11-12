from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataAPIChangeEvent")


@_attrs_define
class DataAPIChangeEvent:
    """One recorded row change. The id is the change's UUID in the database; the platform derives the producer key data-
    api.change.ID from it.

    """

    id: str
    type_: str
    """SCHEMA.TABLE.inserted"""
    data: Any
    """The change"""
    schema_version: str | Unset = UNSET
    """Version captured with this row change; unchanged on retry."""
    time: datetime.datetime | Unset = UNSET
    """When the change was recorded."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        type_ = self.type_

        data = self.data

        schema_version = self.schema_version

        time: str | Unset = UNSET
        if not isinstance(self.time, Unset):
            time = self.time.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "type": type_,
                "data": data,
            }
        )
        if schema_version is not UNSET:
            field_dict["schema_version"] = schema_version
        if time is not UNSET:
            field_dict["time"] = time

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        type_ = d.pop("type")

        data = d.pop("data")

        schema_version = d.pop("schema_version", UNSET)

        _time = d.pop("time", UNSET)
        time: datetime.datetime | Unset
        if isinstance(_time, Unset):
            time = UNSET
        else:
            time = datetime.datetime.fromisoformat(_time)

        data_api_change_event = cls(
            id=id,
            type_=type_,
            data=data,
            schema_version=schema_version,
            time=time,
        )

        return data_api_change_event
