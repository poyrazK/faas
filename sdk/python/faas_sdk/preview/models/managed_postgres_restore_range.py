from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresRestoreRange")


@_attrs_define
class ManagedPostgresRestoreRange:
    """The span a point-in-time restore from this database can target now. Absent while the database cannot be restored
    from.

    """

    earliest: datetime.datetime
    """The later of the database's creation and the start of its restore window."""
    latest: datetime.datetime
    """When the response was made; a restore point must be before it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        earliest = self.earliest.isoformat()

        latest = self.latest.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "earliest": earliest,
                "latest": latest,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        earliest = datetime.datetime.fromisoformat(d.pop("earliest"))

        latest = datetime.datetime.fromisoformat(d.pop("latest"))

        managed_postgres_restore_range = cls(
            earliest=earliest,
            latest=latest,
        )

        managed_postgres_restore_range.additional_properties = d
        return managed_postgres_restore_range

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
