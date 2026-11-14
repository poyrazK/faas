from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_index_key import DataPipelineIndexKey


T = TypeVar("T", bound="DataPipelineIndexAdvice")


@_attrs_define
class DataPipelineIndexAdvice:
    """Frozen metadata-based mirror index advice. Size is a conservative estimate, not measured query performance. Explicit
    approval permits a transactional build for tables up to 64 MiB, indexes up to 32 MiB, at most eight owned indexes
    per pipeline, with a two-second deadline and 250 ms lock timeout. Failures roll back and require a fresh preview.

    """

    id: str
    state: str
    preview_revision: int
    table: str
    keys: list[DataPipelineIndexKey]
    can_apply: bool
    message: str
    index_name: str | Unset = UNSET
    statement: str | Unset = UNSET
    table_bytes: int | Unset = UNSET
    estimated_index_bytes: int | Unset = UNSET
    storage_budget_bytes: int | Unset = UNSET
    applied_bytes: int | Unset = UNSET
    reason: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        state = self.state

        preview_revision = self.preview_revision

        table = self.table

        keys = []
        for keys_item_data in self.keys:
            keys_item = keys_item_data.to_dict()
            keys.append(keys_item)

        can_apply = self.can_apply

        message = self.message

        index_name = self.index_name

        statement = self.statement

        table_bytes = self.table_bytes

        estimated_index_bytes = self.estimated_index_bytes

        storage_budget_bytes = self.storage_budget_bytes

        applied_bytes = self.applied_bytes

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "state": state,
                "preview_revision": preview_revision,
                "table": table,
                "keys": keys,
                "can_apply": can_apply,
                "message": message,
            }
        )
        if index_name is not UNSET:
            field_dict["index_name"] = index_name
        if statement is not UNSET:
            field_dict["statement"] = statement
        if table_bytes is not UNSET:
            field_dict["table_bytes"] = table_bytes
        if estimated_index_bytes is not UNSET:
            field_dict["estimated_index_bytes"] = estimated_index_bytes
        if storage_budget_bytes is not UNSET:
            field_dict["storage_budget_bytes"] = storage_budget_bytes
        if applied_bytes is not UNSET:
            field_dict["applied_bytes"] = applied_bytes
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_index_key import DataPipelineIndexKey

        d = dict(src_dict)
        id = d.pop("id")

        state = d.pop("state")

        preview_revision = d.pop("preview_revision")

        table = d.pop("table")

        keys = []
        _keys = d.pop("keys")
        for keys_item_data in _keys:
            keys_item = DataPipelineIndexKey.from_dict(keys_item_data)

            keys.append(keys_item)

        can_apply = d.pop("can_apply")

        message = d.pop("message")

        index_name = d.pop("index_name", UNSET)

        statement = d.pop("statement", UNSET)

        table_bytes = d.pop("table_bytes", UNSET)

        estimated_index_bytes = d.pop("estimated_index_bytes", UNSET)

        storage_budget_bytes = d.pop("storage_budget_bytes", UNSET)

        applied_bytes = d.pop("applied_bytes", UNSET)

        reason = d.pop("reason", UNSET)

        data_pipeline_index_advice = cls(
            id=id,
            state=state,
            preview_revision=preview_revision,
            table=table,
            keys=keys,
            can_apply=can_apply,
            message=message,
            index_name=index_name,
            statement=statement,
            table_bytes=table_bytes,
            estimated_index_bytes=estimated_index_bytes,
            storage_budget_bytes=storage_budget_bytes,
            applied_bytes=applied_bytes,
            reason=reason,
        )

        return data_pipeline_index_advice
