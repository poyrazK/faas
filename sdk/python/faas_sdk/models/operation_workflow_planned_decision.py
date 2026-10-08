from attrs import define
from dataclasses import asdict
from ..business_decisions import OperationBusinessDecision
@define
class OperationWorkflowPlannedDecision:
    milestone: str
    decision: OperationBusinessDecision
    def to_dict(self): return {"milestone":self.milestone,"decision":asdict(self.decision)}
    @classmethod
    def from_dict(cls, data): return cls(milestone=data["milestone"],decision=OperationBusinessDecision(**data["decision"]))
