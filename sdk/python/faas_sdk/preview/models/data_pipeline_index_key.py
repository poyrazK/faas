from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineIndexKey")


@_attrs_define
class DataPipelineIndexKey:
    """DataPipelineIndexKey wire representation for pipeline operation."""

    column: str
    descending: bool | Unset = UNSET
    tenant_expression: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        column = self.column

        descending = self.descending

        tenant_expression = self.tenant_expression

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "column": column,
            }
        )
        if descending is not UNSET:
            field_dict["descending"] = descending
        if tenant_expression is not UNSET:
            field_dict["tenant_expression"] = tenant_expression

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        column = d.pop("column")

        descending = d.pop("descending", UNSET)

        tenant_expression = d.pop("tenant_expression", UNSET)

        data_pipeline_index_key = cls(
            column=column,
            descending=descending,
            tenant_expression=tenant_expression,
        )

        return data_pipeline_index_key
