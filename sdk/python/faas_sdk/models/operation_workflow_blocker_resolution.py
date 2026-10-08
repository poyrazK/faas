from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID
from attrs import define
T = TypeVar("T", bound="OperationWorkflowBlockerResolution")
@define
class OperationWorkflowBlockerResolution:
    """Application explanation linked to one exact prior blocker report."""
    code: str
    operation: str
    description: str
    blocker_operation_id: UUID
    blocker_report_id: UUID
    blocker_revision: int
    def to_dict(self) -> dict[str, Any]:
        return {"code": self.code, "operation": self.operation, "description": self.description,
                "blocker_operation_id": str(self.blocker_operation_id), "blocker_report_id": str(self.blocker_report_id), "blocker_revision": self.blocker_revision}
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(code=src_dict["code"], operation=src_dict["operation"], description=src_dict["description"],
                   blocker_operation_id=UUID(src_dict["blocker_operation_id"]), blocker_report_id=UUID(src_dict["blocker_report_id"]), blocker_revision=src_dict["blocker_revision"])
