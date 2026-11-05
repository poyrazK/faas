from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_subscription_events_item import (
    OperationWorkflowReadinessSubscriptionEventsItem,
    check_operation_workflow_readiness_subscription_events_item,
)
from ..models.operation_workflow_readiness_subscription_status import (
    OperationWorkflowReadinessSubscriptionStatus,
    check_operation_workflow_readiness_subscription_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_digest_policy import OperationWorkflowNextActionDigestPolicy
    from ..models.operation_workflow_playbook_evidence_watch import OperationWorkflowPlaybookEvidenceWatch
    from ..models.operation_workflow_readiness_impact_watch import OperationWorkflowReadinessImpactWatch


T = TypeVar("T", bound="OperationWorkflowReadinessSubscription")


@_attrs_define
class OperationWorkflowReadinessSubscription:
    """Durable subscription configuration and most recent successful bounded evaluation; deletion cancels future
    evaluations and removes its inbox.

    """

    app_id: UUID
    """OperationWorkflowReadinessSubscription.app_id. Application UUID; required in customer requests and omitted
    in account requests."""
    scope: str
    """OperationWorkflowReadinessSubscription.scope. Application environment scope."""
    name: str
    """OperationWorkflowReadinessSubscription.name. Subscription name."""
    events: list[OperationWorkflowReadinessSubscriptionEventsItem]
    """OperationWorkflowReadinessSubscription.events. Readiness inbox event selectors; defaults to all four when
    omitted."""
    id: UUID
    """OperationWorkflowReadinessSubscription.id. Subscription UUID."""
    created_at: datetime.datetime
    """OperationWorkflowReadinessSubscription.created_at. Subscription creation time."""
    evaluated_at: datetime.datetime
    """OperationWorkflowReadinessSubscription.evaluated_at. Most recent successful evaluation time."""
    status: OperationWorkflowReadinessSubscriptionStatus
    """OperationWorkflowReadinessSubscription.status. Evaluation health; retrying preserves the last successful
    baseline."""
    watched_transition_count: int
    """OperationWorkflowReadinessSubscription.watched_transition_count. Current saved edges within the 100-edge
    watch budget."""
    coverage_incomplete: bool
    """OperationWorkflowReadinessSubscription.coverage_incomplete. Discovery, evidence previews or saved-edge
    capacity are incomplete; no exhaustive delivery guarantee is inferred."""
    latest_sequence: int
    """OperationWorkflowReadinessSubscription.latest_sequence. Latest committed inbox sequence."""
    digest_next_at: datetime.datetime | Unset = UNSET
    """Next digest evaluation time. Evaluation occurs on a worker pass at or after this time; failures retry
    without consuming the cadence."""
    digest_evaluated_at: datetime.datetime | Unset = UNSET
    """Most recent successful digest evaluation, including silent evaluations with no changed items."""
    digest_delivered_at: datetime.datetime | Unset = UNSET
    """Most recent changed-item digest delivery. This does not acknowledge or fulfill source work."""
    next_action_digest: OperationWorkflowNextActionDigestPolicy | Unset = UNSET
    """Fixed team digest selection and cadence. Owner is both the exact action-owner filter and inbox recipient
    label, not proof of team membership. Overdue actions, deadlines within the configured approaching window and
    unaccepted handoffs qualify. Unchanged delivered items are suppressed."""
    business_impact_watches: list[OperationWorkflowReadinessImpactWatch] | Unset = UNSET
    """OperationWorkflowReadinessSubscription.business_impact_watches. Retained impact observations and coverage
    status for configured protected results."""
    playbook_evidence_watches: list[OperationWorkflowPlaybookEvidenceWatch] | Unset = UNSET
    """OperationWorkflowReadinessSubscription.playbook_evidence_watches. Durable opt-in checklist evidence
    monitoring, independent of readiness discovery filters."""
    tenant_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessSubscription.tenant_id. Optional account-only customer selector; customer identity
    derives from authentication."""
    workflow: str | Unset = UNSET
    """OperationWorkflowReadinessSubscription.workflow. Optional declared workflow selector."""
    target_operation: str | Unset = UNSET
    """OperationWorkflowReadinessSubscription.target_operation. Optional transition operation selector."""
    owner: str | Unset = UNSET
    """OperationWorkflowReadinessSubscription.owner. Optional blocker or pending verification owner selector."""
    unassigned: bool | Unset = UNSET
    """OperationWorkflowReadinessSubscription.unassigned. Select ownerless blockers or verification obligations."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        name = self.name

        events = []
        for events_item_data in self.events:
            events_item: str = events_item_data
            events.append(events_item)

        id = str(self.id)

        created_at = self.created_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        status: str = self.status

        watched_transition_count = self.watched_transition_count

        coverage_incomplete = self.coverage_incomplete

        latest_sequence = self.latest_sequence

        digest_next_at: str | Unset = UNSET
        if not isinstance(self.digest_next_at, Unset):
            digest_next_at = self.digest_next_at.isoformat()

        digest_evaluated_at: str | Unset = UNSET
        if not isinstance(self.digest_evaluated_at, Unset):
            digest_evaluated_at = self.digest_evaluated_at.isoformat()

        digest_delivered_at: str | Unset = UNSET
        if not isinstance(self.digest_delivered_at, Unset):
            digest_delivered_at = self.digest_delivered_at.isoformat()

        next_action_digest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.next_action_digest, Unset):
            next_action_digest = self.next_action_digest.to_dict()

        business_impact_watches: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.business_impact_watches, Unset):
            business_impact_watches = []
            for business_impact_watches_item_data in self.business_impact_watches:
                business_impact_watches_item = business_impact_watches_item_data.to_dict()
                business_impact_watches.append(business_impact_watches_item)

        playbook_evidence_watches: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.playbook_evidence_watches, Unset):
            playbook_evidence_watches = []
            for playbook_evidence_watches_item_data in self.playbook_evidence_watches:
                playbook_evidence_watches_item = playbook_evidence_watches_item_data.to_dict()
                playbook_evidence_watches.append(playbook_evidence_watches_item)

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        workflow = self.workflow

        target_operation = self.target_operation

        owner = self.owner

        unassigned = self.unassigned

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "name": name,
                "events": events,
                "id": id,
                "created_at": created_at,
                "evaluated_at": evaluated_at,
                "status": status,
                "watched_transition_count": watched_transition_count,
                "coverage_incomplete": coverage_incomplete,
                "latest_sequence": latest_sequence,
            }
        )
        if digest_next_at is not UNSET:
            field_dict["digest_next_at"] = digest_next_at
        if digest_evaluated_at is not UNSET:
            field_dict["digest_evaluated_at"] = digest_evaluated_at
        if digest_delivered_at is not UNSET:
            field_dict["digest_delivered_at"] = digest_delivered_at
        if next_action_digest is not UNSET:
            field_dict["next_action_digest"] = next_action_digest
        if business_impact_watches is not UNSET:
            field_dict["business_impact_watches"] = business_impact_watches
        if playbook_evidence_watches is not UNSET:
            field_dict["playbook_evidence_watches"] = playbook_evidence_watches
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if target_operation is not UNSET:
            field_dict["target_operation"] = target_operation
        if owner is not UNSET:
            field_dict["owner"] = owner
        if unassigned is not UNSET:
            field_dict["unassigned"] = unassigned

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_digest_policy import OperationWorkflowNextActionDigestPolicy
        from ..models.operation_workflow_playbook_evidence_watch import OperationWorkflowPlaybookEvidenceWatch
        from ..models.operation_workflow_readiness_impact_watch import OperationWorkflowReadinessImpactWatch

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        name = d.pop("name")

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = check_operation_workflow_readiness_subscription_events_item(events_item_data)

            events.append(events_item)

        id = UUID(d.pop("id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        status = check_operation_workflow_readiness_subscription_status(d.pop("status"))

        watched_transition_count = d.pop("watched_transition_count")

        coverage_incomplete = d.pop("coverage_incomplete")

        latest_sequence = d.pop("latest_sequence")

        _digest_next_at = d.pop("digest_next_at", UNSET)
        digest_next_at: datetime.datetime | Unset
        if isinstance(_digest_next_at, Unset):
            digest_next_at = UNSET
        else:
            digest_next_at = datetime.datetime.fromisoformat(_digest_next_at)

        _digest_evaluated_at = d.pop("digest_evaluated_at", UNSET)
        digest_evaluated_at: datetime.datetime | Unset
        if isinstance(_digest_evaluated_at, Unset):
            digest_evaluated_at = UNSET
        else:
            digest_evaluated_at = datetime.datetime.fromisoformat(_digest_evaluated_at)

        _digest_delivered_at = d.pop("digest_delivered_at", UNSET)
        digest_delivered_at: datetime.datetime | Unset
        if isinstance(_digest_delivered_at, Unset):
            digest_delivered_at = UNSET
        else:
            digest_delivered_at = datetime.datetime.fromisoformat(_digest_delivered_at)

        _next_action_digest = d.pop("next_action_digest", UNSET)
        next_action_digest: OperationWorkflowNextActionDigestPolicy | Unset
        if isinstance(_next_action_digest, Unset):
            next_action_digest = UNSET
        else:
            next_action_digest = OperationWorkflowNextActionDigestPolicy.from_dict(_next_action_digest)

        _business_impact_watches = d.pop("business_impact_watches", UNSET)
        business_impact_watches: list[OperationWorkflowReadinessImpactWatch] | Unset = UNSET
        if _business_impact_watches is not UNSET:
            business_impact_watches = []
            for business_impact_watches_item_data in _business_impact_watches:
                business_impact_watches_item = OperationWorkflowReadinessImpactWatch.from_dict(
                    business_impact_watches_item_data
                )

                business_impact_watches.append(business_impact_watches_item)

        _playbook_evidence_watches = d.pop("playbook_evidence_watches", UNSET)
        playbook_evidence_watches: list[OperationWorkflowPlaybookEvidenceWatch] | Unset = UNSET
        if _playbook_evidence_watches is not UNSET:
            playbook_evidence_watches = []
            for playbook_evidence_watches_item_data in _playbook_evidence_watches:
                playbook_evidence_watches_item = OperationWorkflowPlaybookEvidenceWatch.from_dict(
                    playbook_evidence_watches_item_data
                )

                playbook_evidence_watches.append(playbook_evidence_watches_item)

        _tenant_id = d.pop("tenant_id", UNSET)
        tenant_id: UUID | Unset
        if isinstance(_tenant_id, Unset):
            tenant_id = UNSET
        else:
            tenant_id = UUID(_tenant_id)

        workflow = d.pop("workflow", UNSET)

        target_operation = d.pop("target_operation", UNSET)

        owner = d.pop("owner", UNSET)

        unassigned = d.pop("unassigned", UNSET)

        operation_workflow_readiness_subscription = cls(
            app_id=app_id,
            scope=scope,
            name=name,
            events=events,
            id=id,
            created_at=created_at,
            evaluated_at=evaluated_at,
            status=status,
            watched_transition_count=watched_transition_count,
            coverage_incomplete=coverage_incomplete,
            latest_sequence=latest_sequence,
            digest_next_at=digest_next_at,
            digest_evaluated_at=digest_evaluated_at,
            digest_delivered_at=digest_delivered_at,
            next_action_digest=next_action_digest,
            business_impact_watches=business_impact_watches,
            playbook_evidence_watches=playbook_evidence_watches,
            tenant_id=tenant_id,
            workflow=workflow,
            target_operation=target_operation,
            owner=owner,
            unassigned=unassigned,
        )

        return operation_workflow_readiness_subscription
