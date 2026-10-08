from __future__ import annotations
import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_attention_entry import OperationWorkflowAttentionEntry
T = TypeVar("T", bound="OperationWorkflowAttentionResponse")
@define
class OperationWorkflowAttentionResponse:
    items: list[OperationWorkflowAttentionEntry]
    evaluated_at: datetime.datetime
    next_cursor: str | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result = {"items": [item.to_dict() for item in self.items], "evaluated_at": self.evaluated_at.isoformat()}
        if not isinstance(self.next_cursor, Unset):
            result["next_cursor"] = self.next_cursor
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(items=[OperationWorkflowAttentionEntry.from_dict(item) for item in src_dict["items"]],
                   evaluated_at=datetime.datetime.fromisoformat(src_dict["evaluated_at"]), next_cursor=src_dict.get("next_cursor", UNSET))
