from attrs import define
from dataclasses import asdict
from ..business_invariants import OperationBusinessInvariant
@define
class OperationWorkflowPlannedInvariant:
    milestone: str
    invariant: OperationBusinessInvariant
    def to_dict(self): return {"milestone":self.milestone,"invariant":asdict(self.invariant)}
    @classmethod
    def from_dict(cls,data): return cls(milestone=data["milestone"],invariant=OperationBusinessInvariant(**data["invariant"]))
