from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
import datetime
from .operation_subject import OperationSubject
from .operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness
T=TypeVar("T",bound="OperationWorkflowReadinessResponse")
@define
class OperationWorkflowReadinessResponse:
    subject: OperationSubject
    workflow: str
    instance_id: str
    evaluated_at: datetime.datetime
    readiness: OperationWorkflowTransitionReadiness
    def to_dict(self)->dict[str,Any]:
        return {"subject":self.subject.to_dict(),"workflow":self.workflow,"instance_id":self.instance_id,"evaluated_at":self.evaluated_at.isoformat(),"readiness":self.readiness.to_dict()}
    @classmethod
    def from_dict(cls:type[T],src_dict:Mapping[str,Any])->T:
        return cls(subject=OperationSubject.from_dict(src_dict["subject"]),workflow=src_dict["workflow"],instance_id=src_dict["instance_id"],evaluated_at=datetime.datetime.fromisoformat(src_dict["evaluated_at"].replace("Z", "+00:00")),readiness=OperationWorkflowTransitionReadiness.from_dict(src_dict["readiness"]))
