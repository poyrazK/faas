from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from .operation_workflow_attention_stats import OperationWorkflowAttentionStats
T = TypeVar("T", bound="OperationWorkflowAttentionGroup")
@define
class OperationWorkflowAttentionGroup:
    value: str
    stats: OperationWorkflowAttentionStats
    def to_dict(self) -> dict[str, Any]: return {"value":self.value,"stats":self.stats.to_dict()}
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T: return cls(value=src_dict["value"],stats=OperationWorkflowAttentionStats.from_dict(src_dict["stats"]))
