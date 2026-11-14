from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataSourceColumn")


@_attrs_define
class DataSourceColumn:
    """Column metadata only; no stored customer values are read."""

    name: str
    type_: str
    nullable: bool
    primary_key: bool
    unsupported: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        type_ = self.type_

        nullable = self.nullable

        primary_key = self.primary_key

        unsupported = self.unsupported

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "type": type_,
                "nullable": nullable,
                "primary_key": primary_key,
            }
        )
        if unsupported is not UNSET:
            field_dict["unsupported"] = unsupported

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        type_ = d.pop("type")

        nullable = d.pop("nullable")

        primary_key = d.pop("primary_key")

        unsupported = d.pop("unsupported", UNSET)

        data_source_column = cls(
            name=name,
            type_=type_,
            nullable=nullable,
            primary_key=primary_key,
            unsupported=unsupported,
        )

        return data_source_column
