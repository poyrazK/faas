"""Application-reported reversals linked to confirmed retained effect facts."""
from dataclasses import asdict, dataclass
import re
from .business_effects import OperationBusinessEffect
@dataclass(frozen=True)
class OperationBusinessEffectReference:
    operation_id: str
    milestone_id: str
@dataclass(frozen=True)
class OperationBusinessCompensation:
    workflow: str
    instance_id: str
    state: str
    operation: str
    code: str
    version: str
    status: str
    source_effect: OperationBusinessEffectReference
    description: str
    reference: str = ""
    def to_payload(self):
        if self.status not in ("required","pending","failed","confirmed"): raise ValueError("Invalid compensation status")
        OperationBusinessEffect(workflow=self.workflow,instance_id=self.instance_id,state=self.state,operation=self.operation,code=self.code,version=self.version,status="pending" if self.status=="required" else self.status,description=self.description,reference=self.reference).to_payload()
        for identity in (self.source_effect.operation_id,self.source_effect.milestone_id):
            if not isinstance(identity,str) or not re.fullmatch(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",identity) or identity=="00000000-0000-0000-0000-000000000000":
                raise ValueError("Compensation source requires canonical nonzero UUIDs")
        result=asdict(self)
        if not self.reference: result.pop("reference")
        return {"kind":"gregale.business-compensation.v1","compensation":result}
