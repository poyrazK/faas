from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineIndexOrder")


@_attrs_define
class DataPipelineIndexOrder:
    """DataPipelineIndexOrder wire representation for pipeline operation."""

    column: str
    descending: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        column = self.column

        descending = self.descending

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "column": column,
            }
        )
        if descending is not UNSET:
            field_dict["descending"] = descending

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        column = d.pop("column")

        descending = d.pop("descending", UNSET)

        data_pipeline_index_order = cls(
            column=column,
            descending=descending,
        )

        return data_pipeline_index_order
