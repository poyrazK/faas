from __future__ import annotations
import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_attention_group import OperationWorkflowAttentionGroup
from .operation_workflow_attention_stats import OperationWorkflowAttentionStats
T = TypeVar("T", bound="OperationWorkflowAttentionSummary")
@define
class OperationWorkflowAttentionSummary:
    group_by: str
    evaluated_at: datetime.datetime
    totals: OperationWorkflowAttentionStats
    groups: list[OperationWorkflowAttentionGroup]
    next_cursor: str | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result = {"group_by":self.group_by,"evaluated_at":self.evaluated_at.isoformat(),"totals":self.totals.to_dict(),"groups":[g.to_dict() for g in self.groups]}
        if self.next_cursor is not UNSET: result["next_cursor"] = self.next_cursor
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(group_by=src_dict["group_by"],evaluated_at=datetime.datetime.fromisoformat(src_dict["evaluated_at"]),totals=OperationWorkflowAttentionStats.from_dict(src_dict["totals"]),groups=[OperationWorkflowAttentionGroup.from_dict(g) for g in src_dict["groups"]],next_cursor=src_dict.get("next_cursor",UNSET))
