from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID
from attrs import define
from ..types import UNSET, Unset
from .operation_subject import OperationSubject
from .operation_workflow_state import OperationWorkflowState
T = TypeVar("T", bound="OperationWorkflowOutcomeEntry")
@define
class OperationWorkflowOutcomeEntry:
    app_id: UUID
    scope: str
    subject: OperationSubject
    operation_id: UUID
    state: OperationWorkflowState
    platform_tenant_id: UUID | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result = {"app_id": str(self.app_id), "scope": self.scope, "subject": self.subject.to_dict(),
                  "operation_id": str(self.operation_id), "state": self.state.to_dict()}
        if not isinstance(self.platform_tenant_id, Unset):
            result["platform_tenant_id"] = str(self.platform_tenant_id)
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(app_id=UUID(src_dict["app_id"]), scope=src_dict["scope"], subject=OperationSubject.from_dict(src_dict["subject"]),
                   operation_id=UUID(src_dict["operation_id"]), state=OperationWorkflowState.from_dict(src_dict["state"]),
                   platform_tenant_id=UUID(src_dict["platform_tenant_id"]) if src_dict.get("platform_tenant_id") else UNSET)
