from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
    from ..models.operation_workflow_playbook_evidence_change import OperationWorkflowPlaybookEvidenceChange
    from ..models.operation_workflow_playbook_outcome_change import OperationWorkflowPlaybookOutcomeChange


T = TypeVar("T", bound="OperationWorkflowPlaybookEvidenceEvent")


@_attrs_define
class OperationWorkflowPlaybookEvidenceEvent:
    """OperationWorkflowPlaybookEvidenceEvent. Opt-in durable checklist evidence monitoring; advisory observations preserve
    human reviews and application business authority.

    """

    watch_id: UUID
    """OperationWorkflowPlaybookEvidenceEvent.watch_id. Pinned case identity or retained watch metadata."""
    progress_id: UUID
    """OperationWorkflowPlaybookEvidenceEvent.progress_id. Pinned case identity or retained watch metadata."""
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    """OperationWorkflowPlaybookEvidenceEvent.workflow. Pinned case identity or retained watch metadata."""
    instance_id: str
    """OperationWorkflowPlaybookEvidenceEvent.instance_id. Pinned case identity or retained watch metadata."""
    contract_version: int
    """OperationWorkflowPlaybookEvidenceEvent.contract_version. Pinned case identity or retained watch metadata."""
    playbook_id: str
    """OperationWorkflowPlaybookEvidenceEvent.playbook_id. Pinned case identity or retained watch metadata."""
    playbook_version: int
    """OperationWorkflowPlaybookEvidenceEvent.playbook_version. Pinned case identity or retained watch metadata."""
    checked_at: datetime.datetime
    """OperationWorkflowPlaybookEvidenceEvent.checked_at. Observed case evidence clock; this is polling, not an
    application transaction timestamp."""
    changes: list[OperationWorkflowPlaybookEvidenceChange]
    """OperationWorkflowPlaybookEvidenceEvent.changes. Grouped changes in requirement status or diagnostic reason;
    excludes observation clocks and report counters."""
    deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceEvent.deadline_at. Original expected-result deadline when configured."""
    business_outcome: OperationWorkflowPlaybookBusinessOutcome | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""
    outcome_change: OperationWorkflowPlaybookOutcomeChange | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeChange. Saved before/after playbook result observation, separate from
    checklist review and business causation."""
    platform_tenant_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceEvent.platform_tenant_id. Exact evidence customer, included only for
    operator watches and deliveries."""

    def to_dict(self) -> dict[str, Any]:
        watch_id = str(self.watch_id)

        progress_id = str(self.progress_id)

        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        contract_version = self.contract_version

        playbook_id = self.playbook_id

        playbook_version = self.playbook_version

        checked_at = self.checked_at.isoformat()

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        business_outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_outcome, Unset):
            business_outcome = self.business_outcome.to_dict()

        outcome_change: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome_change, Unset):
            outcome_change = self.outcome_change.to_dict()

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "watch_id": watch_id,
                "progress_id": progress_id,
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "contract_version": contract_version,
                "playbook_id": playbook_id,
                "playbook_version": playbook_version,
                "checked_at": checked_at,
                "changes": changes,
            }
        )
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if business_outcome is not UNSET:
            field_dict["business_outcome"] = business_outcome
        if outcome_change is not UNSET:
            field_dict["outcome_change"] = outcome_change
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
        from ..models.operation_workflow_playbook_evidence_change import OperationWorkflowPlaybookEvidenceChange
        from ..models.operation_workflow_playbook_outcome_change import OperationWorkflowPlaybookOutcomeChange

        d = dict(src_dict)
        watch_id = UUID(d.pop("watch_id"))

        progress_id = UUID(d.pop("progress_id"))

        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        contract_version = d.pop("contract_version")

        playbook_id = d.pop("playbook_id")

        playbook_version = d.pop("playbook_version")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = OperationWorkflowPlaybookEvidenceChange.from_dict(changes_item_data)

            changes.append(changes_item)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        _business_outcome = d.pop("business_outcome", UNSET)
        business_outcome: OperationWorkflowPlaybookBusinessOutcome | Unset
        if isinstance(_business_outcome, Unset):
            business_outcome = UNSET
        else:
            business_outcome = OperationWorkflowPlaybookBusinessOutcome.from_dict(_business_outcome)

        _outcome_change = d.pop("outcome_change", UNSET)
        outcome_change: OperationWorkflowPlaybookOutcomeChange | Unset
        if isinstance(_outcome_change, Unset):
            outcome_change = UNSET
        else:
            outcome_change = OperationWorkflowPlaybookOutcomeChange.from_dict(_outcome_change)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_playbook_evidence_event = cls(
            watch_id=watch_id,
            progress_id=progress_id,
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            contract_version=contract_version,
            playbook_id=playbook_id,
            playbook_version=playbook_version,
            checked_at=checked_at,
            changes=changes,
            deadline_at=deadline_at,
            business_outcome=business_outcome,
            outcome_change=outcome_change,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_playbook_evidence_event
