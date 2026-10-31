from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_review_decision_decision import (
    WorkflowRecoveryReviewDecisionDecision,
    check_workflow_recovery_review_decision_decision,
)

T = TypeVar("T", bound="WorkflowRecoveryReviewDecision")


@_attrs_define
class WorkflowRecoveryReviewDecision:
    """Immutable review decision with trusted authenticated actor identity."""

    decision: WorkflowRecoveryReviewDecisionDecision
    reason: str
    decided_at: datetime.datetime
    actor_kind: str
    actor_id: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        decision: str = self.decision

        reason = self.reason

        decided_at = self.decided_at.isoformat()

        actor_kind = self.actor_kind

        actor_id = self.actor_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "decision": decision,
                "reason": reason,
                "decided_at": decided_at,
                "actor_kind": actor_kind,
                "actor_id": actor_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        decision = check_workflow_recovery_review_decision_decision(d.pop("decision"))

        reason = d.pop("reason")

        decided_at = datetime.datetime.fromisoformat(d.pop("decided_at"))

        actor_kind = d.pop("actor_kind")

        actor_id = d.pop("actor_id")

        workflow_recovery_review_decision = cls(
            decision=decision,
            reason=reason,
            decided_at=decided_at,
            actor_kind=actor_kind,
            actor_id=actor_id,
        )

        workflow_recovery_review_decision.additional_properties = d
        return workflow_recovery_review_decision

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
