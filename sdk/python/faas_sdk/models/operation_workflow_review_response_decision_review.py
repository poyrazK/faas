from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_handoff_acceptance import (
        OperationWorkflowReviewResponseHandoffAcceptance,
    )
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
    acceptance_due_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.acceptance_due_at. Acceptance deadline pinned from the actual
    handoff time and effective window, separate from the unchanged decision-review deadline."""
    acceptance_deadline_notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.acceptance_deadline_notified_at. One-time unaccepted handoff
    alert marker, independent of notification acknowledgment."""
    acceptance: OperationWorkflowReviewResponseHandoffAcceptance | Unset = UNSET
    """Immutable acceptance by an authenticated authorized operator for the responsible team, without verifying
    team membership or satisfying the original review deadline."""
    initial_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.initial_owner. Team responsible when this review episode
    began; unknown legacy ownership prevents automatic handoff."""
    owner_changed: bool | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.owner_changed. Ownership changed during this episode,
    including a move away and back; prevents automatic handoff."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.fallback_owner. Distinct fallback team pinned when this
    episode began; future policy edits do not rewrite it."""
    previous_owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.previous_owner. Responsible team immediately before the
    deadline evaluation."""
    owner: str | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.owner. Responsible team after the deadline evaluation, whether
    handoff occurred or was protected."""
    reassigned: bool | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.reassigned. Whether the overdue evaluation transferred both
    response and inbox ownership."""
    due_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.due_at. Immutable deadline pinned from required_at and the
    response review window; legacy flags are backfilled."""
    deadline_notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.deadline_notified_at. One-time overdue alert marker;
    acknowledgment and ordinary edits do not fulfill revalidation."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        changes = self.changes

        health = self.health.to_dict()

        acceptance_due_at: str | Unset = UNSET
        if not isinstance(self.acceptance_due_at, Unset):
            acceptance_due_at = self.acceptance_due_at.isoformat()

        acceptance_deadline_notified_at: str | Unset = UNSET
        if not isinstance(self.acceptance_deadline_notified_at, Unset):
            acceptance_deadline_notified_at = self.acceptance_deadline_notified_at.isoformat()

        acceptance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.acceptance, Unset):
            acceptance = self.acceptance.to_dict()

        initial_owner = self.initial_owner

        owner_changed = self.owner_changed

        fallback_owner = self.fallback_owner

        previous_owner = self.previous_owner

        owner = self.owner

        reassigned = self.reassigned

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        deadline_notified_at: str | Unset = UNSET
        if not isinstance(self.deadline_notified_at, Unset):
            deadline_notified_at = self.deadline_notified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "changes": changes,
                "health": health,
            }
        )
        if acceptance_due_at is not UNSET:
            field_dict["acceptance_due_at"] = acceptance_due_at
        if acceptance_deadline_notified_at is not UNSET:
            field_dict["acceptance_deadline_notified_at"] = acceptance_deadline_notified_at
        if acceptance is not UNSET:
            field_dict["acceptance"] = acceptance
        if initial_owner is not UNSET:
            field_dict["initial_owner"] = initial_owner
        if owner_changed is not UNSET:
            field_dict["owner_changed"] = owner_changed
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if previous_owner is not UNSET:
            field_dict["previous_owner"] = previous_owner
        if owner is not UNSET:
            field_dict["owner"] = owner
        if reassigned is not UNSET:
            field_dict["reassigned"] = reassigned
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if deadline_notified_at is not UNSET:
            field_dict["deadline_notified_at"] = deadline_notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_handoff_acceptance import (
            OperationWorkflowReviewResponseHandoffAcceptance,
        )
        from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth

        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        changes = cast(list[str], d.pop("changes"))

        health = OperationWorkflowReviewResponseHealth.from_dict(d.pop("health"))

        _acceptance_due_at = d.pop("acceptance_due_at", UNSET)
        acceptance_due_at: datetime.datetime | Unset
        if isinstance(_acceptance_due_at, Unset):
            acceptance_due_at = UNSET
        else:
            acceptance_due_at = datetime.datetime.fromisoformat(_acceptance_due_at)

        _acceptance_deadline_notified_at = d.pop("acceptance_deadline_notified_at", UNSET)
        acceptance_deadline_notified_at: datetime.datetime | Unset
        if isinstance(_acceptance_deadline_notified_at, Unset):
            acceptance_deadline_notified_at = UNSET
        else:
            acceptance_deadline_notified_at = datetime.datetime.fromisoformat(_acceptance_deadline_notified_at)

        _acceptance = d.pop("acceptance", UNSET)
        acceptance: OperationWorkflowReviewResponseHandoffAcceptance | Unset
        if isinstance(_acceptance, Unset):
            acceptance = UNSET
        else:
            acceptance = OperationWorkflowReviewResponseHandoffAcceptance.from_dict(_acceptance)

        initial_owner = d.pop("initial_owner", UNSET)

        owner_changed = d.pop("owner_changed", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        previous_owner = d.pop("previous_owner", UNSET)

        owner = d.pop("owner", UNSET)

        reassigned = d.pop("reassigned", UNSET)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        _deadline_notified_at = d.pop("deadline_notified_at", UNSET)
        deadline_notified_at: datetime.datetime | Unset
        if isinstance(_deadline_notified_at, Unset):
            deadline_notified_at = UNSET
        else:
            deadline_notified_at = datetime.datetime.fromisoformat(_deadline_notified_at)

        operation_workflow_review_response_decision_review = cls(
            decision_at=decision_at,
            required_at=required_at,
            changes=changes,
            health=health,
            acceptance_due_at=acceptance_due_at,
            acceptance_deadline_notified_at=acceptance_deadline_notified_at,
            acceptance=acceptance,
            initial_owner=initial_owner,
            owner_changed=owner_changed,
            fallback_owner=fallback_owner,
            previous_owner=previous_owner,
            owner=owner,
            reassigned=reassigned,
            due_at=due_at,
            deadline_notified_at=deadline_notified_at,
        )

        return operation_workflow_review_response_decision_review
