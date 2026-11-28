from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.postgres_change_feed_operations_item import (
    PostgresChangeFeedOperationsItem,
    check_postgres_change_feed_operations_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PostgresChangeFeed")


@_attrs_define
class PostgresChangeFeed:
    """Allowlisted columns of one table whose row changes are captured."""

    name: str
    """Appears in each event type, database.<name>.<operation>."""
    table: str
    """Table name; unqualified names are in the public schema."""
    columns: list[str]
    """The only columns an event carries. The primary key must be listed."""
    operations: list[PostgresChangeFeedOperationsItem] | Unset = UNSET
    """Operations to capture; all three when omitted."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        table = self.table

        columns = self.columns

        operations: list[str] | Unset = UNSET
        if not isinstance(self.operations, Unset):
            operations = []
            for operations_item_data in self.operations:
                operations_item: str = operations_item_data
                operations.append(operations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "table": table,
                "columns": columns,
            }
        )
        if operations is not UNSET:
            field_dict["operations"] = operations

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        table = d.pop("table")

        columns = cast(list[str], d.pop("columns"))

        _operations = d.pop("operations", UNSET)
        operations: list[PostgresChangeFeedOperationsItem] | Unset = UNSET
        if _operations is not UNSET:
            operations = []
            for operations_item_data in _operations:
                operations_item = check_postgres_change_feed_operations_item(operations_item_data)

                operations.append(operations_item)

        postgres_change_feed = cls(
            name=name,
            table=table,
            columns=columns,
            operations=operations,
        )

        postgres_change_feed.additional_properties = d
        return postgres_change_feed

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
