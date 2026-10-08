from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID
from attrs import define
from ..types import UNSET, Unset
from .operation_subject import OperationSubject
from .operation_workflow_related_instance import OperationWorkflowRelatedInstance
from .operation_workflow_state import OperationWorkflowState
T = TypeVar("T", bound="OperationWorkflowAttentionEntry")
@define
class OperationWorkflowAttentionEntry:
    app_id: UUID
    scope: str
    subject: OperationSubject
    operation_id: UUID
    state: OperationWorkflowState
    reasons: list[str]
    dependency_attention: list[OperationWorkflowRelatedInstance] | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result = {"app_id": str(self.app_id), "scope": self.scope, "subject": self.subject.to_dict(),
                  "operation_id": str(self.operation_id), "state": self.state.to_dict(), "reasons": self.reasons}
        if not isinstance(self.platform_tenant_id, Unset):
            result["platform_tenant_id"] = str(self.platform_tenant_id)
        if self.dependency_attention is not UNSET: result["dependency_attention"]=[v.to_dict() for v in self.dependency_attention]
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(app_id=UUID(src_dict["app_id"]), scope=src_dict["scope"], subject=OperationSubject.from_dict(src_dict["subject"]),
                   operation_id=UUID(src_dict["operation_id"]), state=OperationWorkflowState.from_dict(src_dict["state"]),
                   dependency_attention=[OperationWorkflowRelatedInstance.from_dict(v) for v in src_dict["dependency_attention"]] if "dependency_attention" in src_dict else UNSET,
                   reasons=list(src_dict["reasons"]), platform_tenant_id=UUID(src_dict["platform_tenant_id"]) if src_dict.get("platform_tenant_id") else UNSET)
