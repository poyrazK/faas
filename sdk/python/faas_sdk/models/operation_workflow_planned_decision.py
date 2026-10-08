from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_business_decision import OperationBusinessDecision


T = TypeVar("T", bound="OperationWorkflowPlannedDecision")


@_attrs_define
class OperationWorkflowPlannedDecision:
    milestone: str
    decision: OperationBusinessDecision

    def to_dict(self) -> dict[str, Any]:
        milestone = self.milestone

        decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "milestone": milestone,
                "decision": decision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_decision import OperationBusinessDecision

        d = dict(src_dict)
        milestone = d.pop("milestone")

        decision = OperationBusinessDecision.from_dict(d.pop("decision"))

        operation_workflow_planned_decision = cls(
            milestone=milestone,
            decision=decision,
        )

        return operation_workflow_planned_decision
