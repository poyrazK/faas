from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_decision_kind import (
    OperationWorkflowReviewResponseDecisionKind,
    check_operation_workflow_review_response_decision_kind,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth


T = TypeVar("T", bound="OperationWorkflowReviewResponseDecision")


@_attrs_define
class OperationWorkflowReviewResponseDecision:
    """Immutable human decision with accountable actor and the reviewed health snapshot. Application execution remains
    application-owned.

    """

    kind: OperationWorkflowReviewResponseDecisionKind
    """OperationWorkflowReviewResponseDecision.kind. Recorded human intent."""
    reason: str
    """OperationWorkflowReviewResponseDecision.reason. Operator explanation."""
    owner: str
    """OperationWorkflowReviewResponseDecision.owner. Responsible team at decision time."""
    at: datetime.datetime
    """OperationWorkflowReviewResponseDecision.at. Server decision time."""
    actor: str
    """OperationWorkflowReviewResponseDecision.actor. Authenticated operator who saved the decision."""
    health: OperationWorkflowReviewResponseHealth
    """Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not
    business completion. Sampling stops on response resolution."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        reason = self.reason

        owner = self.owner

        at = self.at.isoformat()

        actor = self.actor

        health = self.health.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "reason": reason,
                "owner": owner,
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
        kind = check_operation_workflow_review_response_decision_kind(d.pop("kind"))

        reason = d.pop("reason")

        owner = d.pop("owner")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        health = OperationWorkflowReviewResponseHealth.from_dict(d.pop("health"))

        operation_workflow_review_response_decision = cls(
            kind=kind,
            reason=reason,
            owner=owner,
            at=at,
            actor=actor,
            health=health,
        )

        return operation_workflow_review_response_decision
