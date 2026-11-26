from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresDatabaseUsage")


@_attrs_define
class ManagedPostgresDatabaseUsage:
    """One database's share of the month's compute, with its recorded starts and awake time this month."""

    database_id: str
    name: str
    """Empty for a database deleted since."""
    compute_unit_seconds: int
    starts: int
    awake_seconds: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        name = self.name

        compute_unit_seconds = self.compute_unit_seconds

        starts = self.starts

        awake_seconds = self.awake_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "name": name,
                "compute_unit_seconds": compute_unit_seconds,
                "starts": starts,
                "awake_seconds": awake_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = d.pop("database_id")

        name = d.pop("name")

        compute_unit_seconds = d.pop("compute_unit_seconds")

        starts = d.pop("starts")

        awake_seconds = d.pop("awake_seconds")

        managed_postgres_database_usage = cls(
            database_id=database_id,
            name=name,
            compute_unit_seconds=compute_unit_seconds,
            starts=starts,
            awake_seconds=awake_seconds,
        )

        managed_postgres_database_usage.additional_properties = d
        return managed_postgres_database_usage

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
