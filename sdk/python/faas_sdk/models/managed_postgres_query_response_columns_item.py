from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ManagedPostgresQueryResponseColumnsItem")


@_attrs_define
class ManagedPostgresQueryResponseColumnsItem:
    name: str
    type_: str
    """ PostgreSQL type name, or oid:N for a type the server did not name. """

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        type_ = self.type_

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "type": type_,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        type_ = d.pop("type")

        managed_postgres_query_response_columns_item = cls(
            name=name,
            type_=type_,
        )

        return managed_postgres_query_response_columns_item
