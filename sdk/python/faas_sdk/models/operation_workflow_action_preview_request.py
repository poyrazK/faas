from __future__ import annotations
from attrs import define
from uuid import UUID
from ..types import UNSET, Unset
from .operation_subject import OperationSubject
@define
class OperationWorkflowActionPreviewRequest:
    scope: str
    subject: OperationSubject
    workflow: str
    instance_id: str
    app_id: UUID | Unset = UNSET
    tenant_id: UUID | Unset = UNSET
    operation: str | Unset = UNSET
    state_revision: int | Unset = UNSET
    contract_version: int | Unset = UNSET
    def to_dict(self):
        result={"scope":self.scope,"subject":self.subject.to_dict(),"workflow":self.workflow,"instance_id":self.instance_id}
        for key in ("app_id","tenant_id","operation","state_revision","contract_version"):
            value=getattr(self,key)
            if value is not UNSET: result[key]=str(value) if key.endswith("_id") else value
        return result
    @classmethod
    def from_dict(cls,data):
        values={key:data[key] for key in ("scope","workflow","instance_id")}
        values["subject"]=OperationSubject.from_dict(data["subject"])
        for key in ("app_id","tenant_id","operation","state_revision","contract_version"):
            if key in data: values[key]=UUID(data[key]) if key.endswith("_id") else data[key]
        return cls(**values)
