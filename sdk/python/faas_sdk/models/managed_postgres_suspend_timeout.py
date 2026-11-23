from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresSuspendTimeout")


@_attrs_define
class ManagedPostgresSuspendTimeout:
    """A scale-to-zero database's suspend timeout. Every wake bills at least this much compute."""

    database_id: str
    seconds: int
    """Seconds of inactivity before the database suspends."""
    custom: bool
    """True when the database has its own value; false when it follows the platform default."""
    applied: bool
    """False while the database is running: it takes the value the next time it suspends."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        seconds = self.seconds

        custom = self.custom

        applied = self.applied

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "seconds": seconds,
                "custom": custom,
                "applied": applied,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = d.pop("database_id")

        seconds = d.pop("seconds")

        custom = d.pop("custom")

        applied = d.pop("applied")

        managed_postgres_suspend_timeout = cls(
            database_id=database_id,
            seconds=seconds,
            custom=custom,
            applied=applied,
        )

        managed_postgres_suspend_timeout.additional_properties = d
        return managed_postgres_suspend_timeout

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
