from attrs import define, asdict
from typing import Any
@define
class OperationWorkflowPolicyRequirement:
    milestone: str
    rule_id: str
    rule_version: str
    code: str
    def to_dict(self) -> dict[str, Any]: return asdict(self)
    @classmethod
    def from_dict(cls, data): return cls(**{key:data[key] for key in ("milestone","rule_id","rule_version","code")})
