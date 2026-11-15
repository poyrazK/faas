from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineSchemaColumnChange")


@_attrs_define
class DataPipelineSchemaColumnChange:
    """One exact source column or table change and the worker's compatibility decision."""

    table: str
    column: str
    kind: str
    reason: str
    safe: bool
    before_type: str | Unset = UNSET
    after_type: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        table = self.table

        column = self.column

        kind = self.kind

        reason = self.reason

        safe = self.safe

        before_type = self.before_type

        after_type = self.after_type

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "table": table,
                "column": column,
                "kind": kind,
                "reason": reason,
                "safe": safe,
            }
        )
        if before_type is not UNSET:
            field_dict["before_type"] = before_type
        if after_type is not UNSET:
            field_dict["after_type"] = after_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        table = d.pop("table")

        column = d.pop("column")

        kind = d.pop("kind")

        reason = d.pop("reason")

        safe = d.pop("safe")

        before_type = d.pop("before_type", UNSET)

        after_type = d.pop("after_type", UNSET)

        data_pipeline_schema_column_change = cls(
            table=table,
            column=column,
            kind=kind,
            reason=reason,
            safe=safe,
            before_type=before_type,
            after_type=after_type,
        )

        return data_pipeline_schema_column_change
