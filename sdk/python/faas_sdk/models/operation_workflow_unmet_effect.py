from attrs import define
from .operation_workflow_effect_requirement import OperationWorkflowEffectRequirement
@define
class OperationWorkflowUnmetEffect:
    requirement: OperationWorkflowEffectRequirement
    reason: str
    def to_dict(self): return {"requirement":self.requirement.to_dict(),"reason":self.reason}
    @classmethod
    def from_dict(cls,data): return cls(requirement=OperationWorkflowEffectRequirement.from_dict(data["requirement"]),reason=data["reason"])
