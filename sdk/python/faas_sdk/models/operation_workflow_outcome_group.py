from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
T = TypeVar("T", bound="OperationWorkflowOutcomeGroup")
@define
class OperationWorkflowOutcomeGroup:
    value: str
    workflow_count: int
    def to_dict(self) -> dict[str, Any]: return {"value":self.value,"workflow_count":self.workflow_count}
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T: return cls(value=src_dict["value"],workflow_count=src_dict["workflow_count"])
