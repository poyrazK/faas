from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestDataConnectionCheck")


@_attrs_define
class RequestDataConnectionCheck:
    """Queue inspection of the current credential, optionally including a ready, empty managed mirror."""

    expected_revision: int
    tables: list[str] | Unset = UNSET
    """Exact public table names to inspect, bypassing the default discovery page. Mutually exclusive with search."""
    search: str | Unset = UNSET
    """Discover names containing this literal text before applying the metadata limit. Mutually exclusive with
    tables."""
    database_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        tables: list[str] | Unset = UNSET
        if not isinstance(self.tables, Unset):
            tables = self.tables

        search = self.search

        database_id: str | Unset = UNSET
        if not isinstance(self.database_id, Unset):
            database_id = str(self.database_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
            }
        )
        if tables is not UNSET:
            field_dict["tables"] = tables
        if search is not UNSET:
            field_dict["search"] = search
        if database_id is not UNSET:
            field_dict["database_id"] = database_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        tables = cast(list[str], d.pop("tables", UNSET))

        search = d.pop("search", UNSET)

        _database_id = d.pop("database_id", UNSET)
        database_id: UUID | Unset
        if isinstance(_database_id, Unset):
            database_id = UNSET
        else:
            database_id = UUID(_database_id)

        request_data_connection_check = cls(
            expected_revision=expected_revision,
            tables=tables,
            search=search,
            database_id=database_id,
        )

        return request_data_connection_check
