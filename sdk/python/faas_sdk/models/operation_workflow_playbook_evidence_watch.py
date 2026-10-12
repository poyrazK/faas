from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_evidence_watch_status import (
    OperationWorkflowPlaybookEvidenceWatchStatus,
    check_operation_workflow_playbook_evidence_watch_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
    from ..models.operation_workflow_playbook_evidence_check import OperationWorkflowPlaybookEvidenceCheck


T = TypeVar("T", bound="OperationWorkflowPlaybookEvidenceWatch")


@_attrs_define
class OperationWorkflowPlaybookEvidenceWatch:
    """OperationWorkflowPlaybookEvidenceWatch. Opt-in durable checklist evidence monitoring; advisory observations preserve
    human reviews and application business authority.

    """

    id: UUID
    """OperationWorkflowPlaybookEvidenceWatch.id. Pinned case identity or retained watch metadata."""
    progress_id: UUID
    """OperationWorkflowPlaybookEvidenceWatch.progress_id. Pinned case identity or retained watch metadata."""
    subscription_id: UUID
    """OperationWorkflowPlaybookEvidenceWatch.subscription_id. Existing inbox destination in the same application,
    environment, and caller access boundary."""
    revision: int
    """OperationWorkflowPlaybookEvidenceWatch.revision. Watch configuration revision. Zero creates; polling leaves
    it unchanged."""
    status: OperationWorkflowPlaybookEvidenceWatchStatus
    """OperationWorkflowPlaybookEvidenceWatch.status. Active monitoring or stopped monitoring."""
    owner: str
    """OperationWorkflowPlaybookEvidenceWatch.owner. Responsible team label assigned to new inbox deliveries; no
    external destination."""
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    """OperationWorkflowPlaybookEvidenceWatch.workflow. Pinned case identity or retained watch metadata."""
    instance_id: str
    """OperationWorkflowPlaybookEvidenceWatch.instance_id. Pinned case identity or retained watch metadata."""
    contract_version: int
    """OperationWorkflowPlaybookEvidenceWatch.contract_version. Pinned case identity or retained watch metadata."""
    playbook_id: str
    """OperationWorkflowPlaybookEvidenceWatch.playbook_id. Pinned case identity or retained watch metadata."""
    playbook_version: int
    """OperationWorkflowPlaybookEvidenceWatch.playbook_version. Pinned case identity or retained watch metadata."""
    created_at: datetime.datetime
    """OperationWorkflowPlaybookEvidenceWatch.created_at. Pinned case identity or retained watch metadata."""
    updated_at: datetime.datetime
    """OperationWorkflowPlaybookEvidenceWatch.updated_at. Pinned case identity or retained watch metadata."""
    updated_by: str
    """OperationWorkflowPlaybookEvidenceWatch.updated_by. Authenticated configuration actor, or system actor for
    automatic monitoring stop."""
    evaluated_at: datetime.datetime
    """OperationWorkflowPlaybookEvidenceWatch.evaluated_at. Last successful case evidence observation clock."""
    items: list[OperationWorkflowPlaybookEvidenceCheck]
    """OperationWorkflowPlaybookEvidenceWatch.items. Latest retained requirement observations, separate from
    checklist reviews."""
    outcome_state_revision: int | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceWatch.outcome_state_revision. Highest comparable case report observed for
    the pinned expectation; retained report regressions cannot establish a new result. Preserved across
    configuration edits and unavailable observations."""
    business_outcome: OperationWorkflowPlaybookBusinessOutcome | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""
    stop_reason: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceWatch.stop_reason. Disabled or stopped because the exact checklist was
    closed or unavailable."""
    platform_tenant_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceWatch.platform_tenant_id. Exact evidence customer, included only for
    operator watches and deliveries."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        progress_id = str(self.progress_id)

        subscription_id = str(self.subscription_id)

        revision = self.revision

        status: str = self.status

        owner = self.owner

        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        contract_version = self.contract_version

        playbook_id = self.playbook_id

        playbook_version = self.playbook_version

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        updated_by = self.updated_by

        evaluated_at = self.evaluated_at.isoformat()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        outcome_state_revision = self.outcome_state_revision

        business_outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_outcome, Unset):
            business_outcome = self.business_outcome.to_dict()

        stop_reason = self.stop_reason

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "progress_id": progress_id,
                "subscription_id": subscription_id,
                "revision": revision,
                "status": status,
                "owner": owner,
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "contract_version": contract_version,
                "playbook_id": playbook_id,
                "playbook_version": playbook_version,
                "created_at": created_at,
                "updated_at": updated_at,
                "updated_by": updated_by,
                "evaluated_at": evaluated_at,
                "items": items,
            }
        )
        if outcome_state_revision is not UNSET:
            field_dict["outcome_state_revision"] = outcome_state_revision
        if business_outcome is not UNSET:
            field_dict["business_outcome"] = business_outcome
        if stop_reason is not UNSET:
            field_dict["stop_reason"] = stop_reason
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
        from ..models.operation_workflow_playbook_evidence_check import OperationWorkflowPlaybookEvidenceCheck

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        progress_id = UUID(d.pop("progress_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        revision = d.pop("revision")

        status = check_operation_workflow_playbook_evidence_watch_status(d.pop("status"))

        owner = d.pop("owner")

        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        contract_version = d.pop("contract_version")

        playbook_id = d.pop("playbook_id")

        playbook_version = d.pop("playbook_version")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        updated_by = d.pop("updated_by")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowPlaybookEvidenceCheck.from_dict(items_item_data)

            items.append(items_item)

        outcome_state_revision = d.pop("outcome_state_revision", UNSET)

        _business_outcome = d.pop("business_outcome", UNSET)
        business_outcome: OperationWorkflowPlaybookBusinessOutcome | Unset
        if isinstance(_business_outcome, Unset):
            business_outcome = UNSET
        else:
            business_outcome = OperationWorkflowPlaybookBusinessOutcome.from_dict(_business_outcome)

        stop_reason = d.pop("stop_reason", UNSET)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_playbook_evidence_watch = cls(
            id=id,
            progress_id=progress_id,
            subscription_id=subscription_id,
            revision=revision,
            status=status,
            owner=owner,
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            contract_version=contract_version,
            playbook_id=playbook_id,
            playbook_version=playbook_version,
            created_at=created_at,
            updated_at=updated_at,
            updated_by=updated_by,
            evaluated_at=evaluated_at,
            items=items,
            outcome_state_revision=outcome_state_revision,
            business_outcome=business_outcome,
            stop_reason=stop_reason,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_playbook_evidence_watch
