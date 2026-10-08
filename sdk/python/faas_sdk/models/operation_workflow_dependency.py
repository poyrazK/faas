from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
T=TypeVar("T",bound="OperationWorkflowDependency")
@define
class OperationWorkflowDependency:
    subject_type: str
    subject_id: str
    workflow: str
    instance_id: str
    required_outcome_code: str | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result={"subject_type":self.subject_type,"subject_id":self.subject_id,"workflow":self.workflow,"instance_id":self.instance_id}
        if self.required_outcome_code is not UNSET: result["required_outcome_code"]=self.required_outcome_code
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(subject_type=src_dict["subject_type"],subject_id=src_dict["subject_id"],workflow=src_dict["workflow"],instance_id=src_dict["instance_id"],required_outcome_code=src_dict.get("required_outcome_code",UNSET))
