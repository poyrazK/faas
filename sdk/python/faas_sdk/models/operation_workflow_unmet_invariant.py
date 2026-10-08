from attrs import define
from .operation_workflow_invariant_requirement import OperationWorkflowInvariantRequirement
@define
class OperationWorkflowUnmetInvariant:
    requirement: OperationWorkflowInvariantRequirement
    reason: str
    def to_dict(self): return {"requirement":self.requirement.to_dict(),"reason":self.reason}
    @classmethod
    def from_dict(cls,data): return cls(requirement=OperationWorkflowInvariantRequirement.from_dict(data["requirement"]),reason=data["reason"])
