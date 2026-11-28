from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth


T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationActionReview")


@_attrs_define
class OperationWorkflowReviewResponseEscalationActionReview:
    """Most recent explicit scoped observation of immutable completion evidence. Source indices refer to the completion
    evidence order. A clear observation does not establish causality or business completion; observation is not atomic
    with concurrent application reports. History preserves prior review snapshots within existing retention bounds.

    """

    completed_at: datetime.datetime
    at: datetime.datetime
    actor: str
    health: OperationWorkflowReviewResponseHealth
    """Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not
    business completion. Sampling stops on response resolution."""

    def to_dict(self) -> dict[str, Any]:
        completed_at = self.completed_at.isoformat()

        at = self.at.isoformat()

        actor = self.actor

        health = self.health.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "completed_at": completed_at,
                "at": at,
                "actor": actor,
                "health": health,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth

        d = dict(src_dict)
        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        health = OperationWorkflowReviewResponseHealth.from_dict(d.pop("health"))

        operation_workflow_review_response_escalation_action_review = cls(
            completed_at=completed_at,
            at=at,
            actor=actor,
            health=health,
        )

        return operation_workflow_review_response_escalation_action_review
