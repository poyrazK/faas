from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_outcome_entry import OperationWorkflowOutcomeEntry


T = TypeVar("T", bound="OperationWorkflowOutcomesResponse")


@_attrs_define
class OperationWorkflowOutcomesResponse:
    items: list[OperationWorkflowOutcomeEntry]
    evaluated_at: datetime.datetime
    next_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        evaluated_at = self.evaluated_at.isoformat()

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "evaluated_at": evaluated_at,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_outcome_entry import OperationWorkflowOutcomeEntry

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowOutcomeEntry.from_dict(items_item_data)

            items.append(items_item)

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        next_cursor = d.pop("next_cursor", UNSET)

        operation_workflow_outcomes_response = cls(
            items=items,
            evaluated_at=evaluated_at,
            next_cursor=next_cursor,
        )

        return operation_workflow_outcomes_response
