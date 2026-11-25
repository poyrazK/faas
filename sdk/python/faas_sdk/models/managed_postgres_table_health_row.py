from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresTableHealthRow")


@_attrs_define
class ManagedPostgresTableHealthRow:
    """Storage, dead-row estimates, and vacuum observations for one relation."""

    schema: str
    name: str
    table_bytes: int
    """Table storage including TOAST and auxiliary forks."""
    index_bytes: int
    """Space used by indexes attached to the table."""
    estimated_dead_rows: int
    """The PostgreSQL statistics estimate of dead tuples."""
    changes_since_analyze: int
    """Estimated rows modified since the last analyze."""
    xid_age: int
    """Transaction ID age of the older of the table and its TOAST relation."""
    last_vacuum_at: datetime.datetime | Unset = UNSET
    """Most recent recorded manual or automatic vacuum."""
    last_analyze_at: datetime.datetime | Unset = UNSET
    """Most recent recorded manual or automatic analyze."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schema = self.schema

        name = self.name

        table_bytes = self.table_bytes

        index_bytes = self.index_bytes

        estimated_dead_rows = self.estimated_dead_rows

        changes_since_analyze = self.changes_since_analyze

        xid_age = self.xid_age

        last_vacuum_at: str | Unset = UNSET
        if not isinstance(self.last_vacuum_at, Unset):
            last_vacuum_at = self.last_vacuum_at.isoformat()

        last_analyze_at: str | Unset = UNSET
        if not isinstance(self.last_analyze_at, Unset):
            last_analyze_at = self.last_analyze_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schema": schema,
                "name": name,
                "table_bytes": table_bytes,
                "index_bytes": index_bytes,
                "estimated_dead_rows": estimated_dead_rows,
                "changes_since_analyze": changes_since_analyze,
                "xid_age": xid_age,
            }
        )
        if last_vacuum_at is not UNSET:
            field_dict["last_vacuum_at"] = last_vacuum_at
        if last_analyze_at is not UNSET:
            field_dict["last_analyze_at"] = last_analyze_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        schema = d.pop("schema")

        name = d.pop("name")

        table_bytes = d.pop("table_bytes")

        index_bytes = d.pop("index_bytes")

        estimated_dead_rows = d.pop("estimated_dead_rows")

        changes_since_analyze = d.pop("changes_since_analyze")

        xid_age = d.pop("xid_age")

        _last_vacuum_at = d.pop("last_vacuum_at", UNSET)
        last_vacuum_at: datetime.datetime | Unset
        if isinstance(_last_vacuum_at, Unset):
            last_vacuum_at = UNSET
        else:
            last_vacuum_at = datetime.datetime.fromisoformat(_last_vacuum_at)

        _last_analyze_at = d.pop("last_analyze_at", UNSET)
        last_analyze_at: datetime.datetime | Unset
        if isinstance(_last_analyze_at, Unset):
            last_analyze_at = UNSET
        else:
            last_analyze_at = datetime.datetime.fromisoformat(_last_analyze_at)

        managed_postgres_table_health_row = cls(
            schema=schema,
            name=name,
            table_bytes=table_bytes,
            index_bytes=index_bytes,
            estimated_dead_rows=estimated_dead_rows,
            changes_since_analyze=changes_since_analyze,
            xid_age=xid_age,
            last_vacuum_at=last_vacuum_at,
            last_analyze_at=last_analyze_at,
        )

        managed_postgres_table_health_row.additional_properties = d
        return managed_postgres_table_health_row

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
