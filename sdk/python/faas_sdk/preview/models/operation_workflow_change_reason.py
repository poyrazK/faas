from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_business_decision import OperationBusinessDecision


T = TypeVar("T", bound="OperationWorkflowChangeReason")


@_attrs_define
class OperationWorkflowChangeReason:
    """Optional application-reported explanation tied to a business decision fact in the same transaction and Operation and
    workflow instance and pinned version. It describes application reasoning and grants no authority.

    """

    milestone_id: UUID
    decision: OperationBusinessDecision
    """Application-reported business decision with a public explanation and optional exact policy rule identity."""

    def to_dict(self) -> dict[str, Any]:
        milestone_id = str(self.milestone_id)

        decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "milestone_id": milestone_id,
                "decision": decision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_decision import OperationBusinessDecision

        d = dict(src_dict)
        milestone_id = UUID(d.pop("milestone_id"))

        decision = OperationBusinessDecision.from_dict(d.pop("decision"))

        operation_workflow_change_reason = cls(
            milestone_id=milestone_id,
            decision=decision,
        )

        return operation_workflow_change_reason
