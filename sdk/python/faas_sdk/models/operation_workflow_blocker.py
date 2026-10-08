from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
T = TypeVar("T", bound="OperationWorkflowBlocker")
@define
class OperationWorkflowBlocker:
    """Public application-reported reason a target Operation must wait."""
    code: str
    description: str
    operation: str
    first_observed_at: str | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result = {"code": self.code, "description": self.description, "operation": self.operation}
        if self.first_observed_at is not UNSET: result["first_observed_at"] = self.first_observed_at
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(code=src_dict["code"], description=src_dict["description"], operation=src_dict["operation"], first_observed_at=src_dict.get("first_observed_at", UNSET))
