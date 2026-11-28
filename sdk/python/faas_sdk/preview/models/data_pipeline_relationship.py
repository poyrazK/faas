from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataPipelineRelationship")


@_attrs_define
class DataPipelineRelationship:
    """Read-only computed relationship joining selected source columns to the target complete primary key. Both rows remain
    subject to RLS and exact tenant equality; forged RPC row attributes do not grant access.

    """

    name: str
    source_table: str
    target_table: str
    source_columns: list[str]
    target_columns: list[str]

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        source_table = self.source_table

        target_table = self.target_table

        source_columns = self.source_columns

        target_columns = self.target_columns

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "source_table": source_table,
                "target_table": target_table,
                "source_columns": source_columns,
                "target_columns": target_columns,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        source_table = d.pop("source_table")

        target_table = d.pop("target_table")

        source_columns = cast(list[str], d.pop("source_columns"))

        target_columns = cast(list[str], d.pop("target_columns"))

        data_pipeline_relationship = cls(
            name=name,
            source_table=source_table,
            target_table=target_table,
            source_columns=source_columns,
            target_columns=target_columns,
        )

        return data_pipeline_relationship
