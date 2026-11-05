from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision
    from ..models.workflow_recovery_policy_review_evidence_repairs import WorkflowRecoveryPolicyReviewEvidenceRepairs
    from ..models.workflow_recovery_policy_review_parent_evidence_repairs import (
        WorkflowRecoveryPolicyReviewParentEvidenceRepairs,
    )
    from ..models.workflow_recovery_response_plan import WorkflowRecoveryResponsePlan
    from ..models.workflow_recovery_review_automatic_execution import WorkflowRecoveryReviewAutomaticExecution
    from ..models.workflow_recovery_review_completion_notification import WorkflowRecoveryReviewCompletionNotification
    from ..models.workflow_recovery_review_decision import WorkflowRecoveryReviewDecision
    from ..models.workflow_recovery_review_notification import WorkflowRecoveryReviewNotification


T = TypeVar("T", bound="WorkflowRecoveryPolicyReview")


@_attrs_define
class WorkflowRecoveryPolicyReview:
    """Immutable policy evaluation with its settings snapshot and optional prepared response plan. Repeated unchanged
    decisions are deduplicated; retained reviews are capped at 1000 per app.

    """

    id: UUID
    app_id: UUID
    job_id: UUID
    policy_version: int
    policy: WorkflowRecoveryPolicy
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    observed_at: datetime.datetime
    decisions: list[WorkflowRecoveryPolicyDecision]
    parent_evidence_repairs: WorkflowRecoveryPolicyReviewParentEvidenceRepairs | Unset = UNSET
    evidence_repairs: WorkflowRecoveryPolicyReviewEvidenceRepairs | Unset = UNSET
    automatic_execution: WorkflowRecoveryReviewAutomaticExecution | Unset = UNSET
    """Automatic retry execution records for the frozen review plan. Manual application is recorded through the
    existing keyed child receipts and is not charged to automatic execution budgets."""
    plan: WorkflowRecoveryResponsePlan | Unset = UNSET
    """Saved reviewed actions pinned to an app and parent recovery job. Explicit apply uses stable per-action
    identifiers. Actions commit independently."""
    review_decision: WorkflowRecoveryReviewDecision | Unset = UNSET
    """Immutable review decision with trusted authenticated actor identity."""
    notification: WorkflowRecoveryReviewNotification | Unset = UNSET
    """One durable review notification capture. Delivery outcomes are in the webhook delivery ledger; queued
    capture does not prove HTTP acknowledgement. Frozen receiver identifiers prevent replay to newly configured
    subscriptions."""
    completion_notification: WorkflowRecoveryReviewCompletionNotification | Unset = UNSET
    """Immutable final plan result and completion receiver capture. Queued means a durable event was prepared; HTTP
    acknowledgement is recorded separately in the delivery ledger."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        job_id = str(self.job_id)

        policy_version = self.policy_version

        policy = self.policy.to_dict()

        observed_at = self.observed_at.isoformat()

        decisions = []
        for decisions_item_data in self.decisions:
            decisions_item = decisions_item_data.to_dict()
            decisions.append(decisions_item)

        parent_evidence_repairs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.parent_evidence_repairs, Unset):
            parent_evidence_repairs = self.parent_evidence_repairs.to_dict()

        evidence_repairs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence_repairs, Unset):
            evidence_repairs = self.evidence_repairs.to_dict()

        automatic_execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.automatic_execution, Unset):
            automatic_execution = self.automatic_execution.to_dict()

        plan: dict[str, Any] | Unset = UNSET
        if not isinstance(self.plan, Unset):
            plan = self.plan.to_dict()

        review_decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_decision, Unset):
            review_decision = self.review_decision.to_dict()

        notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.notification, Unset):
            notification = self.notification.to_dict()

        completion_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion_notification, Unset):
            completion_notification = self.completion_notification.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "job_id": job_id,
                "policy_version": policy_version,
                "policy": policy,
                "observed_at": observed_at,
                "decisions": decisions,
            }
        )
        if parent_evidence_repairs is not UNSET:
            field_dict["parent_evidence_repairs"] = parent_evidence_repairs
        if evidence_repairs is not UNSET:
            field_dict["evidence_repairs"] = evidence_repairs
        if automatic_execution is not UNSET:
            field_dict["automatic_execution"] = automatic_execution
        if plan is not UNSET:
            field_dict["plan"] = plan
        if review_decision is not UNSET:
            field_dict["review_decision"] = review_decision
        if notification is not UNSET:
            field_dict["notification"] = notification
        if completion_notification is not UNSET:
            field_dict["completion_notification"] = completion_notification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
        from ..models.workflow_recovery_policy_decision import WorkflowRecoveryPolicyDecision
        from ..models.workflow_recovery_policy_review_evidence_repairs import (
            WorkflowRecoveryPolicyReviewEvidenceRepairs,
        )
        from ..models.workflow_recovery_policy_review_parent_evidence_repairs import (
            WorkflowRecoveryPolicyReviewParentEvidenceRepairs,
        )
        from ..models.workflow_recovery_response_plan import WorkflowRecoveryResponsePlan
        from ..models.workflow_recovery_review_automatic_execution import WorkflowRecoveryReviewAutomaticExecution
        from ..models.workflow_recovery_review_completion_notification import (
            WorkflowRecoveryReviewCompletionNotification,
        )
        from ..models.workflow_recovery_review_decision import WorkflowRecoveryReviewDecision
        from ..models.workflow_recovery_review_notification import WorkflowRecoveryReviewNotification

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        job_id = UUID(d.pop("job_id"))

        policy_version = d.pop("policy_version")

        policy = WorkflowRecoveryPolicy.from_dict(d.pop("policy"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        decisions = []
        _decisions = d.pop("decisions")
        for decisions_item_data in _decisions:
            decisions_item = WorkflowRecoveryPolicyDecision.from_dict(decisions_item_data)

            decisions.append(decisions_item)

        _parent_evidence_repairs = d.pop("parent_evidence_repairs", UNSET)
        parent_evidence_repairs: WorkflowRecoveryPolicyReviewParentEvidenceRepairs | Unset
        if isinstance(_parent_evidence_repairs, Unset):
            parent_evidence_repairs = UNSET
        else:
            parent_evidence_repairs = WorkflowRecoveryPolicyReviewParentEvidenceRepairs.from_dict(
                _parent_evidence_repairs
            )

        _evidence_repairs = d.pop("evidence_repairs", UNSET)
        evidence_repairs: WorkflowRecoveryPolicyReviewEvidenceRepairs | Unset
        if isinstance(_evidence_repairs, Unset):
            evidence_repairs = UNSET
        else:
            evidence_repairs = WorkflowRecoveryPolicyReviewEvidenceRepairs.from_dict(_evidence_repairs)

        _automatic_execution = d.pop("automatic_execution", UNSET)
        automatic_execution: WorkflowRecoveryReviewAutomaticExecution | Unset
        if isinstance(_automatic_execution, Unset):
            automatic_execution = UNSET
        else:
            automatic_execution = WorkflowRecoveryReviewAutomaticExecution.from_dict(_automatic_execution)

        _plan = d.pop("plan", UNSET)
        plan: WorkflowRecoveryResponsePlan | Unset
        if isinstance(_plan, Unset):
            plan = UNSET
        else:
            plan = WorkflowRecoveryResponsePlan.from_dict(_plan)

        _review_decision = d.pop("review_decision", UNSET)
        review_decision: WorkflowRecoveryReviewDecision | Unset
        if isinstance(_review_decision, Unset):
            review_decision = UNSET
        else:
            review_decision = WorkflowRecoveryReviewDecision.from_dict(_review_decision)

        _notification = d.pop("notification", UNSET)
        notification: WorkflowRecoveryReviewNotification | Unset
        if isinstance(_notification, Unset):
            notification = UNSET
        else:
            notification = WorkflowRecoveryReviewNotification.from_dict(_notification)

        _completion_notification = d.pop("completion_notification", UNSET)
        completion_notification: WorkflowRecoveryReviewCompletionNotification | Unset
        if isinstance(_completion_notification, Unset):
            completion_notification = UNSET
        else:
            completion_notification = WorkflowRecoveryReviewCompletionNotification.from_dict(_completion_notification)

        workflow_recovery_policy_review = cls(
            id=id,
            app_id=app_id,
            job_id=job_id,
            policy_version=policy_version,
            policy=policy,
            observed_at=observed_at,
            decisions=decisions,
            parent_evidence_repairs=parent_evidence_repairs,
            evidence_repairs=evidence_repairs,
            automatic_execution=automatic_execution,
            plan=plan,
            review_decision=review_decision,
            notification=notification,
            completion_notification=completion_notification,
        )

        workflow_recovery_policy_review.additional_properties = d
        return workflow_recovery_policy_review

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
