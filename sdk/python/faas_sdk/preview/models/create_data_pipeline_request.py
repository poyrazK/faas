from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateDataPipelineRequest")


@_attrs_define
class CreateDataPipelineRequest:
    """Selected public tables, dedicated destination and explicitly authorized JWT readers. A fresh passing connection
    check for the current source credential and all selected tables is required; otherwise returns
    data_connection_check_required.

    """

    name: str
    source_connection_id: UUID
    database_id: UUID
    tables: list[str]
    read_subjects: list[str]

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        source_connection_id = str(self.source_connection_id)

        database_id = str(self.database_id)

        tables = self.tables

        read_subjects = self.read_subjects

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "source_connection_id": source_connection_id,
                "database_id": database_id,
                "tables": tables,
                "read_subjects": read_subjects,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        source_connection_id = UUID(d.pop("source_connection_id"))

        database_id = UUID(d.pop("database_id"))

        tables = cast(list[str], d.pop("tables"))

        read_subjects = cast(list[str], d.pop("read_subjects"))

        create_data_pipeline_request = cls(
            name=name,
            source_connection_id=source_connection_id,
            database_id=database_id,
            tables=tables,
            read_subjects=read_subjects,
        )

        return create_data_pipeline_request
