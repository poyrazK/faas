from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectLifecycleExpiration")


@_attrs_define
class ObjectLifecycleExpiration:
    """Exactly one action is required. Date must be a UTC midnight. A false marker action is retained as a no-op."""

    days: int | Unset = UNSET
    date: datetime.datetime | Unset = UNSET
    expired_object_delete_marker: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        days = self.days

        date: str | Unset = UNSET
        if not isinstance(self.date, Unset):
            date = self.date.isoformat()

        expired_object_delete_marker = self.expired_object_delete_marker

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if days is not UNSET:
            field_dict["days"] = days
        if date is not UNSET:
            field_dict["date"] = date
        if expired_object_delete_marker is not UNSET:
            field_dict["expired_object_delete_marker"] = expired_object_delete_marker

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        days = d.pop("days", UNSET)

        _date = d.pop("date", UNSET)
        date: datetime.datetime | Unset
        if isinstance(_date, Unset):
            date = UNSET
        else:
            date = datetime.datetime.fromisoformat(_date)

        expired_object_delete_marker = d.pop("expired_object_delete_marker", UNSET)

        object_lifecycle_expiration = cls(
            days=days,
            date=date,
            expired_object_delete_marker=expired_object_delete_marker,
        )

        return object_lifecycle_expiration
