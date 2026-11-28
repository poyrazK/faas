from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresQueryRequest")


@_attrs_define
class ManagedPostgresQueryRequest:
    """One read-only SQL statement."""

    sql: str
    """ One statement; several are refused. """
    max_rows: int | Unset = UNSET
    """ Rows to return; default 100. """

    def to_dict(self) -> dict[str, Any]:
        sql = self.sql

        max_rows = self.max_rows

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "sql": sql,
            }
        )
        if max_rows is not UNSET:
            field_dict["max_rows"] = max_rows

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sql = d.pop("sql")

        max_rows = d.pop("max_rows", UNSET)

        managed_postgres_query_request = cls(
            sql=sql,
            max_rows=max_rows,
        )

        return managed_postgres_query_request
