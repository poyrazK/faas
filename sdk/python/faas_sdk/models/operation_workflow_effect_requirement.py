from attrs import define, asdict
@define
class OperationWorkflowEffectRequirement:
    milestone: str
    code: str
    version: str
    def to_dict(self): return asdict(self)
    @classmethod
    def from_dict(cls,data): return cls(**{key:data[key] for key in ("milestone","code","version")})
