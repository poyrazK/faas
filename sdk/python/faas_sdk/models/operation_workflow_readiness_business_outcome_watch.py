from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_business_outcome_watch_last_notified_status import (
    OperationWorkflowReadinessBusinessOutcomeWatchLastNotifiedStatus,
    check_operation_workflow_readiness_business_outcome_watch_last_notified_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
    from ..models.operation_workflow_readiness_outcome_escalation_policy import (
        OperationWorkflowReadinessOutcomeEscalationPolicy,
    )
    from ..models.operation_workflow_readiness_outcome_escalation_reference import (
        OperationWorkflowReadinessOutcomeEscalationReference,
    )


T = TypeVar("T", bound="OperationWorkflowReadinessBusinessOutcomeWatch")


@_attrs_define
class OperationWorkflowReadinessBusinessOutcomeWatch:
    """Retained opt-in watch for one source completion business criterion. Current observations remain advisory; deadlines
    are notified once and unchanged status is suppressed. Source inbox retention and subscription deletion end
    monitoring.

    """

    id: UUID
    """OperationWorkflowReadinessBusinessOutcomeWatch.id. Retained advisory business outcome watch metadata; does
    not modify application reports or coordination revisions."""
    revision: int
    """OperationWorkflowReadinessBusinessOutcomeWatch.revision. Retained advisory business outcome watch metadata;
    does not modify application reports or coordination revisions."""
    state_revision: int
    """OperationWorkflowReadinessBusinessOutcomeWatch.state_revision. Retained advisory business outcome watch
    metadata; does not modify application reports or coordination revisions."""
    started_at: datetime.datetime
    """OperationWorkflowReadinessBusinessOutcomeWatch.started_at. Retained advisory business outcome watch
    metadata; does not modify application reports or coordination revisions."""
    deadline_notified: bool
    """OperationWorkflowReadinessBusinessOutcomeWatch.deadline_notified. Retained advisory business outcome watch
    metadata; does not modify application reports or coordination revisions."""
    current: OperationWorkflowReadinessBusinessOutcome
    """Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing
    state, stale reports, contract changes, older revisions and terminal reports without outcome codes remain
    unknown. Explicit outcome codes do not imply causation or authorize execution. Completion snapshots are
    immutable; current checks are live reads."""
    escalation: OperationWorkflowReadinessOutcomeEscalationReference | Unset = UNSET
    """Reference to the single retained escalation notification in the same subscription. Pruning does not recreate
    escalations; links may expire."""
    escalation_policy: OperationWorkflowReadinessOutcomeEscalationPolicy | Unset = UNSET
    """Opt-in deadline escalation policy frozen at completion. Requires an outcome watch and future outcome
    deadline. Schedules one assigned follow-up on the deadline notification; never executes business actions."""
    deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcomeWatch.deadline_at. Retained advisory business outcome watch
    metadata; does not modify application reports or coordination revisions."""
    last_notified_status: OperationWorkflowReadinessBusinessOutcomeWatchLastNotifiedStatus | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcomeWatch.last_notified_status. Retained advisory business outcome
    watch metadata; does not modify application reports or coordination revisions."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revision = self.revision

        state_revision = self.state_revision

        started_at = self.started_at.isoformat()

        deadline_notified = self.deadline_notified

        current = self.current.to_dict()

        escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation, Unset):
            escalation = self.escalation.to_dict()

        escalation_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation_policy, Unset):
            escalation_policy = self.escalation_policy.to_dict()

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        last_notified_status: str | Unset = UNSET
        if not isinstance(self.last_notified_status, Unset):
            last_notified_status = self.last_notified_status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "state_revision": state_revision,
                "started_at": started_at,
                "deadline_notified": deadline_notified,
                "current": current,
            }
        )
        if escalation is not UNSET:
            field_dict["escalation"] = escalation
        if escalation_policy is not UNSET:
            field_dict["escalation_policy"] = escalation_policy
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if last_notified_status is not UNSET:
            field_dict["last_notified_status"] = last_notified_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
        from ..models.operation_workflow_readiness_outcome_escalation_policy import (
            OperationWorkflowReadinessOutcomeEscalationPolicy,
        )
        from ..models.operation_workflow_readiness_outcome_escalation_reference import (
            OperationWorkflowReadinessOutcomeEscalationReference,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revision = d.pop("revision")

        state_revision = d.pop("state_revision")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        deadline_notified = d.pop("deadline_notified")

        current = OperationWorkflowReadinessBusinessOutcome.from_dict(d.pop("current"))

        _escalation = d.pop("escalation", UNSET)
        escalation: OperationWorkflowReadinessOutcomeEscalationReference | Unset
        if isinstance(_escalation, Unset):
            escalation = UNSET
        else:
            escalation = OperationWorkflowReadinessOutcomeEscalationReference.from_dict(_escalation)

        _escalation_policy = d.pop("escalation_policy", UNSET)
        escalation_policy: OperationWorkflowReadinessOutcomeEscalationPolicy | Unset
        if isinstance(_escalation_policy, Unset):
            escalation_policy = UNSET
        else:
            escalation_policy = OperationWorkflowReadinessOutcomeEscalationPolicy.from_dict(_escalation_policy)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        _last_notified_status = d.pop("last_notified_status", UNSET)
        last_notified_status: OperationWorkflowReadinessBusinessOutcomeWatchLastNotifiedStatus | Unset
        if isinstance(_last_notified_status, Unset):
            last_notified_status = UNSET
        else:
            last_notified_status = check_operation_workflow_readiness_business_outcome_watch_last_notified_status(
                _last_notified_status
            )

        operation_workflow_readiness_business_outcome_watch = cls(
            id=id,
            revision=revision,
            state_revision=state_revision,
            started_at=started_at,
            deadline_notified=deadline_notified,
            current=current,
            escalation=escalation,
            escalation_policy=escalation_policy,
            deadline_at=deadline_at,
            last_notified_status=last_notified_status,
        )

        return operation_workflow_readiness_business_outcome_watch
