from __future__ import annotations
from attrs import define
import datetime
from ..types import UNSET, Unset
from .operation_subject import OperationSubject
from .operation_workflow_state import OperationWorkflowState
from .operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness
@define
class OperationWorkflowActionPreviewResponse:
    subject: OperationSubject
    workflow: str
    instance_id: str
    evaluated_at: datetime.datetime
    contract_version: int
    reason: str
    actions: list[OperationWorkflowTransitionReadiness]
    action_count: int
    has_more: bool
    state: OperationWorkflowState | Unset = UNSET
    state_revision: int | Unset = UNSET
    def to_dict(self):
        result={key:getattr(self,key) for key in ("workflow","instance_id","contract_version","reason","action_count","has_more")}
        result["subject"]=self.subject.to_dict();result["evaluated_at"]=self.evaluated_at.isoformat()
        result["actions"]=[v.to_dict() for v in self.actions]
        if self.state is not UNSET: result["state"]=self.state.to_dict()
        if self.state_revision is not UNSET: result["state_revision"]=self.state_revision
        return result
    @classmethod
    def from_dict(cls,data):
        values={key:data[key] for key in ("workflow","instance_id","contract_version","reason","action_count","has_more")}
        return cls(**values,subject=OperationSubject.from_dict(data["subject"]),evaluated_at=datetime.datetime.fromisoformat(data["evaluated_at"].replace("Z","+00:00")),actions=[OperationWorkflowTransitionReadiness.from_dict(v) for v in data["actions"]],state=OperationWorkflowState.from_dict(data["state"]) if "state" in data else UNSET,state_revision=data.get("state_revision",UNSET))
