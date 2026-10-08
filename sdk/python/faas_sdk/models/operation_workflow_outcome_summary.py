from __future__ import annotations
import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_outcome_group import OperationWorkflowOutcomeGroup
T = TypeVar("T", bound="OperationWorkflowOutcomeSummary")
@define
class OperationWorkflowOutcomeSummary:
    group_by: str
    evaluated_at: datetime.datetime
    workflow_count: int
    groups: list[OperationWorkflowOutcomeGroup]
    next_cursor: str | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result = {"group_by":self.group_by,"evaluated_at":self.evaluated_at.isoformat(),"workflow_count":self.workflow_count,"groups":[g.to_dict() for g in self.groups]}
        if self.next_cursor is not UNSET: result["next_cursor"] = self.next_cursor
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(group_by=src_dict["group_by"],evaluated_at=datetime.datetime.fromisoformat(src_dict["evaluated_at"]),workflow_count=src_dict["workflow_count"],groups=[OperationWorkflowOutcomeGroup.from_dict(g) for g in src_dict["groups"]],next_cursor=src_dict.get("next_cursor",UNSET))
