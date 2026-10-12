from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth


T = TypeVar("T", bound="OperationWorkflowReviewResponseDecisionReview")


@_attrs_define
class OperationWorkflowReviewResponseDecisionReview:
    """First changed observation for one explicit response decision. Remains latched until a new explicit decision;
    ordinary edits, acknowledgment and subsequent recovery do not clear it.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseDecisionReview.decision_at. Time of the decision that needs revalidation."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseDecisionReview.required_at. First sampled condition change, committed with
    its one-time inbox alert."""
    changes: list[str]
    """OperationWorkflowReviewResponseDecisionReview.changes. Differences from the immutable reviewed snapshot,
    including unavailable sources without claiming business failure."""
    health: OperationWorkflowReviewResponseHealth
    """Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not
    business completion. Sampling stops on response resolution."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        changes = self.changes

        health = self.health.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "changes": changes,
                "health": health,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth

        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        changes = cast(list[str], d.pop("changes"))

        health = OperationWorkflowReviewResponseHealth.from_dict(d.pop("health"))

        operation_workflow_review_response_decision_review = cls(
            decision_at=decision_at,
            required_at=required_at,
            changes=changes,
            health=health,
        )

        return operation_workflow_review_response_decision_review
