from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_masked_table import ManagedPostgresMaskedTable


T = TypeVar("T", bound="CreateManagedPostgresMaskedBranchRequest")


@_attrs_define
class CreateManagedPostgresMaskedBranchRequest:
    """Source table policies and configuration for creating a masked database copy."""

    name: str
    tables: list[ManagedPostgresMaskedTable]
    ttl_seconds: int | Unset = 7200

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        tables = []
        for tables_item_data in self.tables:
            tables_item = tables_item_data.to_dict()
            tables.append(tables_item)

        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "tables": tables,
            }
        )
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_masked_table import ManagedPostgresMaskedTable

        d = dict(src_dict)
        name = d.pop("name")

        tables = []
        _tables = d.pop("tables")
        for tables_item_data in _tables:
            tables_item = ManagedPostgresMaskedTable.from_dict(tables_item_data)

            tables.append(tables_item)

        ttl_seconds = d.pop("ttl_seconds", UNSET)

        create_managed_postgres_masked_branch_request = cls(
            name=name,
            tables=tables,
            ttl_seconds=ttl_seconds,
        )

        return create_managed_postgres_masked_branch_request
