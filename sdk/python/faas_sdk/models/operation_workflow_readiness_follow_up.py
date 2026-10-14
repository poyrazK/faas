from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_playbook_outcome_escalation import OperationWorkflowPlaybookOutcomeEscalation
    from ..models.operation_workflow_playbook_step_assignment import OperationWorkflowPlaybookStepAssignment
    from ..models.operation_workflow_readiness_coordination import OperationWorkflowReadinessCoordination
    from ..models.operation_workflow_readiness_due_reminder import OperationWorkflowReadinessDueReminder
    from ..models.operation_workflow_readiness_impact_event import OperationWorkflowReadinessImpactEvent
    from ..models.operation_workflow_readiness_outcome_escalation import OperationWorkflowReadinessOutcomeEscalation
    from ..models.operation_workflow_review_response import OperationWorkflowReviewResponse


T = TypeVar("T", bound="OperationWorkflowReadinessFollowUp")


@_attrs_define
class OperationWorkflowReadinessFollowUp:
    """Compact pending follow-up with original inbox references; no application readiness is inferred."""

    subscription_id: UUID
    """OperationWorkflowReadinessFollowUp.subscription_id. Retained notification or subscription identity; tenant
    fields are account-only."""
    subscription_name: str
    """OperationWorkflowReadinessFollowUp.subscription_name. Retained notification or subscription identity; tenant
    fields are account-only."""
    scope: str
    """OperationWorkflowReadinessFollowUp.scope. Retained notification or subscription identity; tenant fields are
    account-only."""
    notification_id: UUID
    """OperationWorkflowReadinessFollowUp.notification_id. Retained notification or subscription identity; tenant
    fields are account-only."""
    kind: str
    """OperationWorkflowReadinessFollowUp.kind. Retained notification or subscription identity; tenant fields are
    account-only."""
    sequence: int
    """OperationWorkflowReadinessFollowUp.sequence. Subscription-local notification sequence used for edits and
    evidence checks."""
    created_at: datetime.datetime
    """OperationWorkflowReadinessFollowUp.created_at. Original notification creation time."""
    coordination: OperationWorkflowReadinessCoordination
    """OperationWorkflowReadinessCoordination. Shared inbox coordination with optimistic revision checks; omission
    of a follow-up time clears it."""
    overdue: bool
    """OperationWorkflowReadinessFollowUp.overdue. Follow-up time is at or before this response evaluation clock."""
    attention_due_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.attention_due_at. Earlier of human check-in and active decision
    revalidation deadline, used for queue ordering and overdue filters; coordination remains unchanged."""
    review_response: OperationWorkflowReviewResponse | Unset = UNSET
    """OperationWorkflowReviewResponse. Human accountability record under the notification coordination revision
    fence, independent of business workflow success."""
    business_impact: OperationWorkflowReadinessImpactEvent | Unset = UNSET
    """OperationWorkflowReadinessImpactEvent. Protected business result monitoring of retained unresolved human
    reviews in a scoped application environment; advisory confidence only."""
    playbook_step: OperationWorkflowPlaybookStepAssignment | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignment. Assigned step attention in the existing inbox and follow-up queue.
    Polling preserves manual coordination and bounded ownership history."""
    playbook_escalation: OperationWorkflowPlaybookOutcomeEscalation | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeEscalation. Retained assigned deadline follow-up evidence. Polling refreshes
    current observations and the first observed success only; acknowledgment, owner, notes, due times, reminders and
    completion remain human coordination."""
    outcome_escalation: OperationWorkflowReadinessOutcomeEscalation | Unset = UNSET
    """Live evidence for an assigned outcome escalation, separate from immutable deadline delivery evidence and
    human coordination. First observed application success is retained as a flag; it never automatically
    acknowledges or completes work. Monitoring ends with source retention."""
    subscription_tenant_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.subscription_tenant_id. Retained notification or subscription identity;
    tenant fields are account-only."""
    platform_tenant_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.platform_tenant_id. Retained notification or subscription identity;
    tenant fields are account-only."""
    workflow: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.workflow. Retained notification or subscription identity; tenant fields
    are account-only."""
    instance_id: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.instance_id. Retained notification or subscription identity; tenant
    fields are account-only."""
    operation: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.operation. Retained notification or subscription identity; tenant fields
    are account-only."""
    from_state: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.from_state. Retained notification or subscription identity; tenant fields
    are account-only."""
    to_state: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUp.to_state. Retained notification or subscription identity; tenant fields
    are account-only."""
    subject: OperationSubject | Unset = UNSET
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    reminder: OperationWorkflowReadinessDueReminder | Unset = UNSET
    """One durable in-app reminder marker for the original notification and follow-up deadline; rescheduling or
    clearing retires it. Does not authorize execution."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        subscription_name = self.subscription_name

        scope = self.scope

        notification_id = str(self.notification_id)

        kind = self.kind

        sequence = self.sequence

        created_at = self.created_at.isoformat()

        coordination = self.coordination.to_dict()

        overdue = self.overdue

        attention_due_at: str | Unset = UNSET
        if not isinstance(self.attention_due_at, Unset):
            attention_due_at = self.attention_due_at.isoformat()

        review_response: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_response, Unset):
            review_response = self.review_response.to_dict()

        business_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_impact, Unset):
            business_impact = self.business_impact.to_dict()

        playbook_step: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_step, Unset):
            playbook_step = self.playbook_step.to_dict()

        playbook_escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook_escalation, Unset):
            playbook_escalation = self.playbook_escalation.to_dict()

        outcome_escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome_escalation, Unset):
            outcome_escalation = self.outcome_escalation.to_dict()

        subscription_tenant_id: str | Unset = UNSET
        if not isinstance(self.subscription_tenant_id, Unset):
            subscription_tenant_id = str(self.subscription_tenant_id)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        workflow = self.workflow

        instance_id = self.instance_id

        operation = self.operation

        from_state = self.from_state

        to_state = self.to_state

        subject: dict[str, Any] | Unset = UNSET
        if not isinstance(self.subject, Unset):
            subject = self.subject.to_dict()

        reminder: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reminder, Unset):
            reminder = self.reminder.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "subscription_name": subscription_name,
                "scope": scope,
                "notification_id": notification_id,
                "kind": kind,
                "sequence": sequence,
                "created_at": created_at,
                "coordination": coordination,
                "overdue": overdue,
            }
        )
        if attention_due_at is not UNSET:
            field_dict["attention_due_at"] = attention_due_at
        if review_response is not UNSET:
            field_dict["review_response"] = review_response
        if business_impact is not UNSET:
            field_dict["business_impact"] = business_impact
        if playbook_step is not UNSET:
            field_dict["playbook_step"] = playbook_step
        if playbook_escalation is not UNSET:
            field_dict["playbook_escalation"] = playbook_escalation
        if outcome_escalation is not UNSET:
            field_dict["outcome_escalation"] = outcome_escalation
        if subscription_tenant_id is not UNSET:
            field_dict["subscription_tenant_id"] = subscription_tenant_id
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if operation is not UNSET:
            field_dict["operation"] = operation
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if to_state is not UNSET:
            field_dict["to_state"] = to_state
        if subject is not UNSET:
            field_dict["subject"] = subject
        if reminder is not UNSET:
            field_dict["reminder"] = reminder

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_playbook_outcome_escalation import OperationWorkflowPlaybookOutcomeEscalation
        from ..models.operation_workflow_playbook_step_assignment import OperationWorkflowPlaybookStepAssignment
        from ..models.operation_workflow_readiness_coordination import OperationWorkflowReadinessCoordination
        from ..models.operation_workflow_readiness_due_reminder import OperationWorkflowReadinessDueReminder
        from ..models.operation_workflow_readiness_impact_event import OperationWorkflowReadinessImpactEvent
        from ..models.operation_workflow_readiness_outcome_escalation import OperationWorkflowReadinessOutcomeEscalation
        from ..models.operation_workflow_review_response import OperationWorkflowReviewResponse

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        subscription_name = d.pop("subscription_name")

        scope = d.pop("scope")

        notification_id = UUID(d.pop("notification_id"))

        kind = d.pop("kind")

        sequence = d.pop("sequence")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        coordination = OperationWorkflowReadinessCoordination.from_dict(d.pop("coordination"))

        overdue = d.pop("overdue")

        _attention_due_at = d.pop("attention_due_at", UNSET)
        attention_due_at: datetime.datetime | Unset
        if isinstance(_attention_due_at, Unset):
            attention_due_at = UNSET
        else:
            attention_due_at = datetime.datetime.fromisoformat(_attention_due_at)

        _review_response = d.pop("review_response", UNSET)
        review_response: OperationWorkflowReviewResponse | Unset
        if isinstance(_review_response, Unset):
            review_response = UNSET
        else:
            review_response = OperationWorkflowReviewResponse.from_dict(_review_response)

        _business_impact = d.pop("business_impact", UNSET)
        business_impact: OperationWorkflowReadinessImpactEvent | Unset
        if isinstance(_business_impact, Unset):
            business_impact = UNSET
        else:
            business_impact = OperationWorkflowReadinessImpactEvent.from_dict(_business_impact)

        _playbook_step = d.pop("playbook_step", UNSET)
        playbook_step: OperationWorkflowPlaybookStepAssignment | Unset
        if isinstance(_playbook_step, Unset):
            playbook_step = UNSET
        else:
            playbook_step = OperationWorkflowPlaybookStepAssignment.from_dict(_playbook_step)

        _playbook_escalation = d.pop("playbook_escalation", UNSET)
        playbook_escalation: OperationWorkflowPlaybookOutcomeEscalation | Unset
        if isinstance(_playbook_escalation, Unset):
            playbook_escalation = UNSET
        else:
            playbook_escalation = OperationWorkflowPlaybookOutcomeEscalation.from_dict(_playbook_escalation)

        _outcome_escalation = d.pop("outcome_escalation", UNSET)
        outcome_escalation: OperationWorkflowReadinessOutcomeEscalation | Unset
        if isinstance(_outcome_escalation, Unset):
            outcome_escalation = UNSET
        else:
            outcome_escalation = OperationWorkflowReadinessOutcomeEscalation.from_dict(_outcome_escalation)

        _subscription_tenant_id = d.pop("subscription_tenant_id", UNSET)
        subscription_tenant_id: UUID | Unset
        if isinstance(_subscription_tenant_id, Unset):
            subscription_tenant_id = UNSET
        else:
            subscription_tenant_id = UUID(_subscription_tenant_id)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        workflow = d.pop("workflow", UNSET)

        instance_id = d.pop("instance_id", UNSET)

        operation = d.pop("operation", UNSET)

        from_state = d.pop("from_state", UNSET)

        to_state = d.pop("to_state", UNSET)

        _subject = d.pop("subject", UNSET)
        subject: OperationSubject | Unset
        if isinstance(_subject, Unset):
            subject = UNSET
        else:
            subject = OperationSubject.from_dict(_subject)

        _reminder = d.pop("reminder", UNSET)
        reminder: OperationWorkflowReadinessDueReminder | Unset
        if isinstance(_reminder, Unset):
            reminder = UNSET
        else:
            reminder = OperationWorkflowReadinessDueReminder.from_dict(_reminder)

        operation_workflow_readiness_follow_up = cls(
            subscription_id=subscription_id,
            subscription_name=subscription_name,
            scope=scope,
            notification_id=notification_id,
            kind=kind,
            sequence=sequence,
            created_at=created_at,
            coordination=coordination,
            overdue=overdue,
            attention_due_at=attention_due_at,
            review_response=review_response,
            business_impact=business_impact,
            playbook_step=playbook_step,
            playbook_escalation=playbook_escalation,
            outcome_escalation=outcome_escalation,
            subscription_tenant_id=subscription_tenant_id,
            platform_tenant_id=platform_tenant_id,
            workflow=workflow,
            instance_id=instance_id,
            operation=operation,
            from_state=from_state,
            to_state=to_state,
            subject=subject,
            reminder=reminder,
        )

        return operation_workflow_readiness_follow_up
