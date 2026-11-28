from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_schema_column_change import DataPipelineSchemaColumnChange


T = TypeVar("T", bound="DataPipelineSchemaReview")


@_attrs_define
class DataPipelineSchemaReview:
    """Worker-generated exact source-definition review with column and table metadata only. Its ID binds the generation,
    recorded baseline and requested source schema.

    """

    id: str
    state: str
    supported: bool
    changes: list[DataPipelineSchemaColumnChange]
    requested_tables: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        state = self.state

        supported = self.supported

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        requested_tables: list[str] | Unset = UNSET
        if not isinstance(self.requested_tables, Unset):
            requested_tables = self.requested_tables

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "state": state,
                "supported": supported,
                "changes": changes,
            }
        )
        if requested_tables is not UNSET:
            field_dict["requested_tables"] = requested_tables

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_schema_column_change import DataPipelineSchemaColumnChange

        d = dict(src_dict)
        id = d.pop("id")

        state = d.pop("state")

        supported = d.pop("supported")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = DataPipelineSchemaColumnChange.from_dict(changes_item_data)

            changes.append(changes_item)

        requested_tables = cast(list[str], d.pop("requested_tables", UNSET))

        data_pipeline_schema_review = cls(
            id=id,
            state=state,
            supported=supported,
            changes=changes,
            requested_tables=requested_tables,
        )

        return data_pipeline_schema_review
