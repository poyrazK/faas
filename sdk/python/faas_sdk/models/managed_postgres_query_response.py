from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.managed_postgres_query_response_columns_item import ManagedPostgresQueryResponseColumnsItem


T = TypeVar("T", bound="ManagedPostgresQueryResponse")


@_attrs_define
class ManagedPostgresQueryResponse:
    """The rows one read-only statement read."""

    database_id: str
    columns: list[ManagedPostgresQueryResponseColumnsItem]
    rows: list[list[None | str]]
    row_count: int
    truncated: bool
    """ True when the row or 1 MiB cap stopped reading before the result ended. """
    duration_ms: int

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        columns = []
        for columns_item_data in self.columns:
            columns_item = columns_item_data.to_dict()
            columns.append(columns_item)

        rows = []
        for rows_item_data in self.rows:
            rows_item = []
            for rows_item_item_data in rows_item_data:
                rows_item_item: None | str
                rows_item_item = rows_item_item_data
                rows_item.append(rows_item_item)

            rows.append(rows_item)

        row_count = self.row_count

        truncated = self.truncated

        duration_ms = self.duration_ms

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "database_id": database_id,
                "columns": columns,
                "rows": rows,
                "row_count": row_count,
                "truncated": truncated,
                "duration_ms": duration_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_query_response_columns_item import ManagedPostgresQueryResponseColumnsItem

        d = dict(src_dict)
        database_id = d.pop("database_id")

        columns = []
        _columns = d.pop("columns")
        for columns_item_data in _columns:
            columns_item = ManagedPostgresQueryResponseColumnsItem.from_dict(columns_item_data)

            columns.append(columns_item)

        rows = []
        _rows = d.pop("rows")
        for rows_item_data in _rows:
            rows_item = []
            _rows_item = rows_item_data
            for rows_item_item_data in _rows_item:

                def _parse_rows_item_item(data: object) -> None | str:
                    if data is None:
                        return data
                    return cast(None | str, data)

                rows_item_item = _parse_rows_item_item(rows_item_item_data)

                rows_item.append(rows_item_item)

            rows.append(rows_item)

        row_count = d.pop("row_count")

        truncated = d.pop("truncated")

        duration_ms = d.pop("duration_ms")

        managed_postgres_query_response = cls(
            database_id=database_id,
            columns=columns,
            rows=rows,
            row_count=row_count,
            truncated=truncated,
            duration_ms=duration_ms,
        )

        return managed_postgres_query_response
