from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.managed_postgres_masked_column import ManagedPostgresMaskedColumn


T = TypeVar("T", bound="ManagedPostgresMaskedTable")


@_attrs_define
class ManagedPostgresMaskedTable:
    """Policy governing which rows and columns are copied from a source table."""

    name: str
    columns: list[ManagedPostgresMaskedColumn]

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        columns = []
        for columns_item_data in self.columns:
            columns_item = columns_item_data.to_dict()
            columns.append(columns_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "columns": columns,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_masked_column import ManagedPostgresMaskedColumn

        d = dict(src_dict)
        name = d.pop("name")

        columns = []
        _columns = d.pop("columns")
        for columns_item_data in _columns:
            columns_item = ManagedPostgresMaskedColumn.from_dict(columns_item_data)

            columns.append(columns_item)

        managed_postgres_masked_table = cls(
            name=name,
            columns=columns,
        )

        return managed_postgres_masked_table
