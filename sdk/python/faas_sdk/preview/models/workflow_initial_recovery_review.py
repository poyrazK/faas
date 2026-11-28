from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.workflow_initial_recovery_review_status import (
    WorkflowInitialRecoveryReviewStatus,
    check_workflow_initial_recovery_review_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_initial_evidence_repair_receipt import WorkflowInitialEvidenceRepairReceipt
    from ..models.workflow_initial_failure import WorkflowInitialFailure
    from ..models.workflow_initial_observation_failure import WorkflowInitialObservationFailure
    from ..models.workflow_initial_recovery_attention import WorkflowInitialRecoveryAttention
    from ..models.workflow_initial_recovery_review_event import WorkflowInitialRecoveryReviewEvent
    from ..models.workflow_recovery_automatic_blocked_notification import WorkflowRecoveryAutomaticBlockedNotification
    from ..models.workflow_recovery_automatic_evaluation import WorkflowRecoveryAutomaticEvaluation
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification
    from ..models.workflow_recovery_review_action_status import WorkflowRecoveryReviewActionStatus
    from ..models.workflow_recovery_review_notification import WorkflowRecoveryReviewNotification
    from ..models.workflow_run_recovery_target import WorkflowRunRecoveryTarget


T = TypeVar("T", bound="WorkflowInitialRecoveryReview")


@_attrs_define
class WorkflowInitialRecoveryReview:
    """Immutable failure and policy snapshot with durable transition history. Pending reviews expire after 24 hours. Fresh
    read status is advisory; submission and pending native admission repeat checks. Preparation and preview-mode manual
    submission each enforce the policy cooldown and hourly action budget; preparation shares the existing parent-review
    budget. Retained for 30 days with at most 1000 initial reviews per app; unresolved automatic submissions remain
    retained until proven final. automatic_initial submission reserves the separate hourly execution budget and one
    unresolved recovery slot per app. Automatic outcomes require saved terminal generation evidence or final admission
    refusal. Submitted job completion and notifications use the native recovery job lifecycle.

    """

    id: UUID
    app_id: UUID
    app: str
    failure: WorkflowInitialFailure
    """One captured generation-zero workflow failure."""
    target: WorkflowRunRecoveryTarget
    """One reviewed workflow run and resume count. Retry children identify the saved parent item. Initial failure
    targets may identify retained generation-zero evidence, which is checked again at creation and admission."""
    policy_version: int
    policy: WorkflowRecoveryPolicy
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    status: WorkflowInitialRecoveryReviewStatus
    reason: str
    prepared_at: datetime.datetime
    expires_at: datetime.datetime
    observed_at: datetime.datetime
    events: list[WorkflowInitialRecoveryReviewEvent]
    polling_notification: WorkflowRecoveryPollingNotification | Unset = UNSET
    """Last captured polling alert receipt. queued means durable outbox admission; no_receivers never replays to
    later subscribers. Successful polling retains the recovered receipt separately from the cleared error metadata.
   """
    evidence_repair: WorkflowInitialEvidenceRepairReceipt | Unset = UNSET
    """Recorded result of an initial recovery evidence repair."""
    automatic_block_reason: str | Unset = UNSET
    automatic_block_sequence: int | Unset = UNSET
    automatic_block_notification: WorkflowRecoveryAutomaticBlockedNotification | Unset = UNSET
    """Latest blocked or recovered evidence capture for a retry or initial-recovery review. Retry sequence
    increases only on a changed evidence diagnosis or proven clearance. Receivers are frozen at capture; queued
    identifies ledger admission rather than successful delivery. No receivers prevents historical replay to later
    subscribers."""
    observation_failure: WorkflowInitialObservationFailure | Unset = UNSET
    """Last deterministic manual observation failure. Retry uses bounded backoff; a successful observation clears
    this metadata. Codes never include database error messages."""
    attention: WorkflowInitialRecoveryAttention | Unset = UNSET
    """Latest committed observation of a submitted manual initial recovery. Reads do not update it. Notification
    receipts describe captured historical transitions."""
    result: WorkflowRecoveryReviewActionStatus | Unset = UNSET
    """Application freshness and execution outcome for one frozen action. A retry is complete only with saved
    terminal generation evidence or a terminal admission refusal."""
    automatic_result: WorkflowRecoveryReviewActionStatus | Unset = UNSET
    """Application freshness and execution outcome for one frozen action. A retry is complete only with saved
    terminal generation evidence or a terminal admission refusal."""
    automatic_evaluation: WorkflowRecoveryAutomaticEvaluation | Unset = UNSET
    """Last committed worker evaluation, never refreshed by reads or notification capture. Waiting_since is the
    first observation of this consecutive wait reason and resets when the reason changes. Absent for legacy or
    unevaluated intents. Pausing preserves historical evidence."""
    job_id: UUID | Unset = UNSET
    submitted_at: datetime.datetime | Unset = UNSET
    notification: WorkflowRecoveryReviewNotification | Unset = UNSET
    """One durable review notification capture. Delivery outcomes are in the webhook delivery ledger; queued
    capture does not prove HTTP acknowledgement. Frozen receiver identifiers prevent replay to newly configured
    subscriptions."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        app = self.app

        failure = self.failure.to_dict()

        target = self.target.to_dict()

        policy_version = self.policy_version

        policy = self.policy.to_dict()

        status: str = self.status

        reason = self.reason

        prepared_at = self.prepared_at.isoformat()

        expires_at = self.expires_at.isoformat()

        observed_at = self.observed_at.isoformat()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        polling_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.polling_notification, Unset):
            polling_notification = self.polling_notification.to_dict()

        evidence_repair: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence_repair, Unset):
            evidence_repair = self.evidence_repair.to_dict()

        automatic_block_reason = self.automatic_block_reason

        automatic_block_sequence = self.automatic_block_sequence

        automatic_block_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.automatic_block_notification, Unset):
            automatic_block_notification = self.automatic_block_notification.to_dict()

        observation_failure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.observation_failure, Unset):
            observation_failure = self.observation_failure.to_dict()

        attention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attention, Unset):
            attention = self.attention.to_dict()

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        automatic_result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.automatic_result, Unset):
            automatic_result = self.automatic_result.to_dict()

        automatic_evaluation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.automatic_evaluation, Unset):
            automatic_evaluation = self.automatic_evaluation.to_dict()

        job_id: str | Unset = UNSET
        if not isinstance(self.job_id, Unset):
            job_id = str(self.job_id)

        submitted_at: str | Unset = UNSET
        if not isinstance(self.submitted_at, Unset):
            submitted_at = self.submitted_at.isoformat()

        notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.notification, Unset):
            notification = self.notification.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "app": app,
                "failure": failure,
                "target": target,
                "policy_version": policy_version,
                "policy": policy,
                "status": status,
                "reason": reason,
                "prepared_at": prepared_at,
                "expires_at": expires_at,
                "observed_at": observed_at,
                "events": events,
            }
        )
        if polling_notification is not UNSET:
            field_dict["polling_notification"] = polling_notification
        if evidence_repair is not UNSET:
            field_dict["evidence_repair"] = evidence_repair
        if automatic_block_reason is not UNSET:
            field_dict["automatic_block_reason"] = automatic_block_reason
        if automatic_block_sequence is not UNSET:
            field_dict["automatic_block_sequence"] = automatic_block_sequence
        if automatic_block_notification is not UNSET:
            field_dict["automatic_block_notification"] = automatic_block_notification
        if observation_failure is not UNSET:
            field_dict["observation_failure"] = observation_failure
        if attention is not UNSET:
            field_dict["attention"] = attention
        if result is not UNSET:
            field_dict["result"] = result
        if automatic_result is not UNSET:
            field_dict["automatic_result"] = automatic_result
        if automatic_evaluation is not UNSET:
            field_dict["automatic_evaluation"] = automatic_evaluation
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if submitted_at is not UNSET:
            field_dict["submitted_at"] = submitted_at
        if notification is not UNSET:
            field_dict["notification"] = notification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_initial_evidence_repair_receipt import WorkflowInitialEvidenceRepairReceipt
        from ..models.workflow_initial_failure import WorkflowInitialFailure
        from ..models.workflow_initial_observation_failure import WorkflowInitialObservationFailure
        from ..models.workflow_initial_recovery_attention import WorkflowInitialRecoveryAttention
        from ..models.workflow_initial_recovery_review_event import WorkflowInitialRecoveryReviewEvent
        from ..models.workflow_recovery_automatic_blocked_notification import (
            WorkflowRecoveryAutomaticBlockedNotification,
        )
        from ..models.workflow_recovery_automatic_evaluation import WorkflowRecoveryAutomaticEvaluation
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
        from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification
        from ..models.workflow_recovery_review_action_status import WorkflowRecoveryReviewActionStatus
        from ..models.workflow_recovery_review_notification import WorkflowRecoveryReviewNotification
        from ..models.workflow_run_recovery_target import WorkflowRunRecoveryTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        app = d.pop("app")

        failure = WorkflowInitialFailure.from_dict(d.pop("failure"))

        target = WorkflowRunRecoveryTarget.from_dict(d.pop("target"))

        policy_version = d.pop("policy_version")

        policy = WorkflowRecoveryPolicy.from_dict(d.pop("policy"))

        status = check_workflow_initial_recovery_review_status(d.pop("status"))

        reason = d.pop("reason")

        prepared_at = datetime.datetime.fromisoformat(d.pop("prepared_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = WorkflowInitialRecoveryReviewEvent.from_dict(events_item_data)

            events.append(events_item)

        _polling_notification = d.pop("polling_notification", UNSET)
        polling_notification: WorkflowRecoveryPollingNotification | Unset
        if isinstance(_polling_notification, Unset):
            polling_notification = UNSET
        else:
            polling_notification = WorkflowRecoveryPollingNotification.from_dict(_polling_notification)

        _evidence_repair = d.pop("evidence_repair", UNSET)
        evidence_repair: WorkflowInitialEvidenceRepairReceipt | Unset
        if isinstance(_evidence_repair, Unset):
            evidence_repair = UNSET
        else:
            evidence_repair = WorkflowInitialEvidenceRepairReceipt.from_dict(_evidence_repair)

        automatic_block_reason = d.pop("automatic_block_reason", UNSET)

        automatic_block_sequence = d.pop("automatic_block_sequence", UNSET)

        _automatic_block_notification = d.pop("automatic_block_notification", UNSET)
        automatic_block_notification: WorkflowRecoveryAutomaticBlockedNotification | Unset
        if isinstance(_automatic_block_notification, Unset):
            automatic_block_notification = UNSET
        else:
            automatic_block_notification = WorkflowRecoveryAutomaticBlockedNotification.from_dict(
                _automatic_block_notification
            )

        _observation_failure = d.pop("observation_failure", UNSET)
        observation_failure: WorkflowInitialObservationFailure | Unset
        if isinstance(_observation_failure, Unset):
            observation_failure = UNSET
        else:
            observation_failure = WorkflowInitialObservationFailure.from_dict(_observation_failure)

        _attention = d.pop("attention", UNSET)
        attention: WorkflowInitialRecoveryAttention | Unset
        if isinstance(_attention, Unset):
            attention = UNSET
        else:
            attention = WorkflowInitialRecoveryAttention.from_dict(_attention)

        _result = d.pop("result", UNSET)
        result: WorkflowRecoveryReviewActionStatus | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = WorkflowRecoveryReviewActionStatus.from_dict(_result)

        _automatic_result = d.pop("automatic_result", UNSET)
        automatic_result: WorkflowRecoveryReviewActionStatus | Unset
        if isinstance(_automatic_result, Unset):
            automatic_result = UNSET
        else:
            automatic_result = WorkflowRecoveryReviewActionStatus.from_dict(_automatic_result)

        _automatic_evaluation = d.pop("automatic_evaluation", UNSET)
        automatic_evaluation: WorkflowRecoveryAutomaticEvaluation | Unset
        if isinstance(_automatic_evaluation, Unset):
            automatic_evaluation = UNSET
        else:
            automatic_evaluation = WorkflowRecoveryAutomaticEvaluation.from_dict(_automatic_evaluation)

        _job_id = d.pop("job_id", UNSET)
        job_id: UUID | Unset
        if isinstance(_job_id, Unset):
            job_id = UNSET
        else:
            job_id = UUID(_job_id)

        _submitted_at = d.pop("submitted_at", UNSET)
        submitted_at: datetime.datetime | Unset
        if isinstance(_submitted_at, Unset):
            submitted_at = UNSET
        else:
            submitted_at = datetime.datetime.fromisoformat(_submitted_at)

        _notification = d.pop("notification", UNSET)
        notification: WorkflowRecoveryReviewNotification | Unset
        if isinstance(_notification, Unset):
            notification = UNSET
        else:
            notification = WorkflowRecoveryReviewNotification.from_dict(_notification)

        workflow_initial_recovery_review = cls(
            id=id,
            app_id=app_id,
            app=app,
            failure=failure,
            target=target,
            policy_version=policy_version,
            policy=policy,
            status=status,
            reason=reason,
            prepared_at=prepared_at,
            expires_at=expires_at,
            observed_at=observed_at,
            events=events,
            polling_notification=polling_notification,
            evidence_repair=evidence_repair,
            automatic_block_reason=automatic_block_reason,
            automatic_block_sequence=automatic_block_sequence,
            automatic_block_notification=automatic_block_notification,
            observation_failure=observation_failure,
            attention=attention,
            result=result,
            automatic_result=automatic_result,
            automatic_evaluation=automatic_evaluation,
            job_id=job_id,
            submitted_at=submitted_at,
            notification=notification,
        )

        return workflow_initial_recovery_review
