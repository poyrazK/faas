from attrs import define
from dataclasses import asdict
from ..business_effects import OperationBusinessEffect
@define
class OperationWorkflowPlannedEffect:
    milestone: str
    effect: OperationBusinessEffect
    def to_dict(self): return {"milestone":self.milestone,"effect":self.effect.to_payload()["effect"]}
    @classmethod
    def from_dict(cls,data): return cls(milestone=data["milestone"],effect=OperationBusinessEffect(**data["effect"]))
