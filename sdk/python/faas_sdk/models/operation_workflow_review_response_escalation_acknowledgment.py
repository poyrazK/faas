from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_escalation_action_review import (
        OperationWorkflowReviewResponseEscalationActionReview,
    )
    from ..models.operation_workflow_review_response_escalation_completion import (
        OperationWorkflowReviewResponseEscalationCompletion,
    )


T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationAcknowledgment")


@_attrs_define
class OperationWorkflowReviewResponseEscalationAcknowledgment:
    """Immutable human escalation acknowledgment with a coordinator commitment, recorded actor and time. Notification
    acknowledgment alone does not create this record. The system overdue marker and explicit human completion may be
    appended without changing commitment fields.

    """

    coordinator: str
    plan: str
    due_at: datetime.datetime
    at: datetime.datetime
    actor: str
    previous_completed_at: datetime.datetime | Unset = UNSET
    """Previous coordinator completion that this explicit revised commitment follows. Original completion remains
    in retained history."""
    basis: OperationWorkflowReviewResponseEscalationActionReview | Unset = UNSET
    """Most recent explicit scoped observation of immutable completion evidence. Source indices refer to the
    completion evidence order. A clear observation does not establish causality or business completion; observation
    is not atomic with concurrent application reports. History preserves prior review snapshots within existing
    retention bounds."""
    completion: OperationWorkflowReviewResponseEscalationCompletion | Unset = UNSET
    """Immutable human completion of escalation coordination work, with linked review references and server actor
    and time. Existing overdue markers remain historical."""
    overdue_notified_at: datetime.datetime | Unset = UNSET
    """One-time follow-up publication marker. Stored atomically with its inbox notice and mirrored in retained
    history; human update timestamps remain unchanged."""

    def to_dict(self) -> dict[str, Any]:
        coordinator = self.coordinator

        plan = self.plan

        due_at = self.due_at.isoformat()

        at = self.at.isoformat()

        actor = self.actor

        previous_completed_at: str | Unset = UNSET
        if not isinstance(self.previous_completed_at, Unset):
            previous_completed_at = self.previous_completed_at.isoformat()

        basis: dict[str, Any] | Unset = UNSET
        if not isinstance(self.basis, Unset):
            basis = self.basis.to_dict()

        completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion, Unset):
            completion = self.completion.to_dict()

        overdue_notified_at: str | Unset = UNSET
        if not isinstance(self.overdue_notified_at, Unset):
            overdue_notified_at = self.overdue_notified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "coordinator": coordinator,
                "plan": plan,
                "due_at": due_at,
                "at": at,
                "actor": actor,
            }
        )
        if previous_completed_at is not UNSET:
            field_dict["previous_completed_at"] = previous_completed_at
        if basis is not UNSET:
            field_dict["basis"] = basis
        if completion is not UNSET:
            field_dict["completion"] = completion
        if overdue_notified_at is not UNSET:
            field_dict["overdue_notified_at"] = overdue_notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_escalation_action_review import (
            OperationWorkflowReviewResponseEscalationActionReview,
        )
        from ..models.operation_workflow_review_response_escalation_completion import (
            OperationWorkflowReviewResponseEscalationCompletion,
        )

        d = dict(src_dict)
        coordinator = d.pop("coordinator")

        plan = d.pop("plan")

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        _previous_completed_at = d.pop("previous_completed_at", UNSET)
        previous_completed_at: datetime.datetime | Unset
        if isinstance(_previous_completed_at, Unset):
            previous_completed_at = UNSET
        else:
            previous_completed_at = datetime.datetime.fromisoformat(_previous_completed_at)

        _basis = d.pop("basis", UNSET)
        basis: OperationWorkflowReviewResponseEscalationActionReview | Unset
        if isinstance(_basis, Unset):
            basis = UNSET
        else:
            basis = OperationWorkflowReviewResponseEscalationActionReview.from_dict(_basis)

        _completion = d.pop("completion", UNSET)
        completion: OperationWorkflowReviewResponseEscalationCompletion | Unset
        if isinstance(_completion, Unset):
            completion = UNSET
        else:
            completion = OperationWorkflowReviewResponseEscalationCompletion.from_dict(_completion)

        _overdue_notified_at = d.pop("overdue_notified_at", UNSET)
        overdue_notified_at: datetime.datetime | Unset
        if isinstance(_overdue_notified_at, Unset):
            overdue_notified_at = UNSET
        else:
            overdue_notified_at = datetime.datetime.fromisoformat(_overdue_notified_at)

        operation_workflow_review_response_escalation_acknowledgment = cls(
            coordinator=coordinator,
            plan=plan,
            due_at=due_at,
            at=at,
            actor=actor,
            previous_completed_at=previous_completed_at,
            basis=basis,
            completion=completion,
            overdue_notified_at=overdue_notified_at,
        )

        return operation_workflow_review_response_escalation_acknowledgment
