from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_postgres_table_health_row import ManagedPostgresTableHealthRow


T = TypeVar("T", bound="ManagedPostgresTableHealthSample")


@_attrs_define
class ManagedPostgresTableHealthSample:
    """Ranked table and leaf-partition measurements observed on the primary."""

    sampled_at: datetime.datetime
    has_more: bool
    tables: list[ManagedPostgresTableHealthRow]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sampled_at = self.sampled_at.isoformat()

        has_more = self.has_more

        tables = []
        for tables_item_data in self.tables:
            tables_item = tables_item_data.to_dict()
            tables.append(tables_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sampled_at": sampled_at,
                "has_more": has_more,
                "tables": tables,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_table_health_row import ManagedPostgresTableHealthRow

        d = dict(src_dict)
        sampled_at = datetime.datetime.fromisoformat(d.pop("sampled_at"))

        has_more = d.pop("has_more")

        tables = []
        _tables = d.pop("tables")
        for tables_item_data in _tables:
            tables_item = ManagedPostgresTableHealthRow.from_dict(tables_item_data)

            tables.append(tables_item)

        managed_postgres_table_health_sample = cls(
            sampled_at=sampled_at,
            has_more=has_more,
            tables=tables,
        )

        managed_postgres_table_health_sample.additional_properties = d
        return managed_postgres_table_health_sample

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
