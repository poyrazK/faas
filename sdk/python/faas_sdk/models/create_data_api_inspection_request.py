from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateDataAPIInspectionRequest")


@_attrs_define
class CreateDataAPIInspectionRequest:
    """Identifies the database binding and table to inspect."""

    database_id: UUID
    binding_id: UUID
    table: str

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        binding_id = str(self.binding_id)

        table = self.table

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "database_id": database_id,
                "binding_id": binding_id,
                "table": table,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        binding_id = UUID(d.pop("binding_id"))

        table = d.pop("table")

        create_data_api_inspection_request = cls(
            database_id=database_id,
            binding_id=binding_id,
            table=table,
        )

        return create_data_api_inspection_request
