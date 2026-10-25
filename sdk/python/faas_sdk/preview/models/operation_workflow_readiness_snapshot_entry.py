from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_snapshot_entry_advisories_item import (
    OperationWorkflowReadinessSnapshotEntryAdvisoriesItem,
    check_operation_workflow_readiness_snapshot_entry_advisories_item,
)
from ..models.operation_workflow_readiness_snapshot_entry_reasons_item import (
    OperationWorkflowReadinessSnapshotEntryReasonsItem,
    check_operation_workflow_readiness_snapshot_entry_reasons_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_readiness_evidence import OperationWorkflowReadinessEvidence


T = TypeVar("T", bound="OperationWorkflowReadinessSnapshotEntry")


@_attrs_define
class OperationWorkflowReadinessSnapshotEntry:
    """One saved transition identity and stable evidence summaries for comparison within the same ownership scope."""

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    """ReadinessSnapshotEntry. Declared workflow name."""
    instance_id: str
    """ReadinessSnapshotEntry. Application workflow instance identifier."""
    operation_id: UUID
    """ReadinessSnapshotEntry. Operation that reported the saved workflow state."""
    state_revision: int
    """ReadinessSnapshotEntry. Application state revision observed at capture."""
    contract_version: int
    """ReadinessSnapshotEntry. Pinned workflow contract version at capture."""
    operation: str
    """ReadinessSnapshotEntry. Target Operation of the saved transition."""
    from_state: str
    """ReadinessSnapshotEntry. Declared source state of the saved transition."""
    to_state: str
    """ReadinessSnapshotEntry. Declared destination state of the saved transition."""
    evaluated_at: datetime.datetime
    """ReadinessSnapshotEntry. Time of the shared per-workflow evidence evaluation or comparison completion."""
    ready: bool
    """ReadinessSnapshotEntry. Declared requirements were met without planned evidence at capture."""
    reasons: list[OperationWorkflowReadinessSnapshotEntryReasonsItem]
    """ReadinessSnapshotEntry. Saved unmet declared requirement codes."""
    advisories: list[OperationWorkflowReadinessSnapshotEntryAdvisoriesItem]
    """ReadinessSnapshotEntry. Saved reporting and verification context; advisories never determine readiness."""
    blocker_codes: list[str]
    """ReadinessSnapshotEntry. Sorted unique saved blocker codes for this target Operation."""
    missing_milestones: list[str]
    """ReadinessSnapshotEntry. Saved missing planned milestone names."""
    missing_dependency_workflows: list[str]
    """ReadinessSnapshotEntry. Saved prerequisite workflows without reported references."""
    pending_verification_count: int
    """ReadinessSnapshotEntry. Exact saved pending verification obligation count before previews."""
    requirements_fingerprint: str
    """ReadinessSnapshotEntry. Digest of pinned contract version and declared edge requirements."""
    blockers_fingerprint: str
    """ReadinessSnapshotEntry. Order-independent digest of all target blocker details."""
    prerequisites_fingerprint: str
    """ReadinessSnapshotEntry. Digest of prerequisite statuses and evidence identities excluding moving ages and
    evaluation clocks."""
    verifications_fingerprint: str
    """ReadinessSnapshotEntry. Order-independent digest of all retained pending target obligations before previews."""
    reporting_fingerprint: str
    """ReadinessSnapshotEntry. Digest of reporting coverage counts and timing advisories excluding pending
    verification."""
    platform_tenant_id: UUID | Unset = UNSET
    """ReadinessSnapshotEntry. Customer UUID present only in account snapshots."""
    evidence: list[OperationWorkflowReadinessEvidence] | Unset = UNSET
    """Bounded saved evidence inventory; older snapshots may omit it."""
    evidence_complete: bool | Unset = UNSET
    """True only when the saved evidence inventory covers every applicable observation."""

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        operation_id = str(self.operation_id)

        state_revision = self.state_revision

        contract_version = self.contract_version

        operation = self.operation

        from_state = self.from_state

        to_state = self.to_state

        evaluated_at = self.evaluated_at.isoformat()

        ready = self.ready

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item: str = reasons_item_data
            reasons.append(reasons_item)

        advisories = []
        for advisories_item_data in self.advisories:
            advisories_item: str = advisories_item_data
            advisories.append(advisories_item)

        blocker_codes = self.blocker_codes

        missing_milestones = self.missing_milestones

        missing_dependency_workflows = self.missing_dependency_workflows

        pending_verification_count = self.pending_verification_count

        requirements_fingerprint = self.requirements_fingerprint

        blockers_fingerprint = self.blockers_fingerprint

        prerequisites_fingerprint = self.prerequisites_fingerprint

        verifications_fingerprint = self.verifications_fingerprint

        reporting_fingerprint = self.reporting_fingerprint

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        evidence: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = []
            for evidence_item_data in self.evidence:
                evidence_item = evidence_item_data.to_dict()
                evidence.append(evidence_item)

        evidence_complete = self.evidence_complete

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "operation_id": operation_id,
                "state_revision": state_revision,
                "contract_version": contract_version,
                "operation": operation,
                "from_state": from_state,
                "to_state": to_state,
                "evaluated_at": evaluated_at,
                "ready": ready,
                "reasons": reasons,
                "advisories": advisories,
                "blocker_codes": blocker_codes,
                "missing_milestones": missing_milestones,
                "missing_dependency_workflows": missing_dependency_workflows,
                "pending_verification_count": pending_verification_count,
                "requirements_fingerprint": requirements_fingerprint,
                "blockers_fingerprint": blockers_fingerprint,
                "prerequisites_fingerprint": prerequisites_fingerprint,
                "verifications_fingerprint": verifications_fingerprint,
                "reporting_fingerprint": reporting_fingerprint,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if evidence is not UNSET:
            field_dict["evidence"] = evidence
        if evidence_complete is not UNSET:
            field_dict["evidence_complete"] = evidence_complete

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_readiness_evidence import OperationWorkflowReadinessEvidence

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        operation_id = UUID(d.pop("operation_id"))

        state_revision = d.pop("state_revision")

        contract_version = d.pop("contract_version")

        operation = d.pop("operation")

        from_state = d.pop("from_state")

        to_state = d.pop("to_state")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        ready = d.pop("ready")

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = check_operation_workflow_readiness_snapshot_entry_reasons_item(reasons_item_data)

            reasons.append(reasons_item)

        advisories = []
        _advisories = d.pop("advisories")
        for advisories_item_data in _advisories:
            advisories_item = check_operation_workflow_readiness_snapshot_entry_advisories_item(advisories_item_data)

            advisories.append(advisories_item)

        blocker_codes = cast(list[str], d.pop("blocker_codes"))

        missing_milestones = cast(list[str], d.pop("missing_milestones"))

        missing_dependency_workflows = cast(list[str], d.pop("missing_dependency_workflows"))

        pending_verification_count = d.pop("pending_verification_count")

        requirements_fingerprint = d.pop("requirements_fingerprint")

        blockers_fingerprint = d.pop("blockers_fingerprint")

        prerequisites_fingerprint = d.pop("prerequisites_fingerprint")

        verifications_fingerprint = d.pop("verifications_fingerprint")

        reporting_fingerprint = d.pop("reporting_fingerprint")

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _evidence = d.pop("evidence", UNSET)
        evidence: list[OperationWorkflowReadinessEvidence] | Unset = UNSET
        if _evidence is not UNSET:
            evidence = []
            for evidence_item_data in _evidence:
                evidence_item = OperationWorkflowReadinessEvidence.from_dict(evidence_item_data)

                evidence.append(evidence_item)

        evidence_complete = d.pop("evidence_complete", UNSET)

        operation_workflow_readiness_snapshot_entry = cls(
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            operation_id=operation_id,
            state_revision=state_revision,
            contract_version=contract_version,
            operation=operation,
            from_state=from_state,
            to_state=to_state,
            evaluated_at=evaluated_at,
            ready=ready,
            reasons=reasons,
            advisories=advisories,
            blocker_codes=blocker_codes,
            missing_milestones=missing_milestones,
            missing_dependency_workflows=missing_dependency_workflows,
            pending_verification_count=pending_verification_count,
            requirements_fingerprint=requirements_fingerprint,
            blockers_fingerprint=blockers_fingerprint,
            prerequisites_fingerprint=prerequisites_fingerprint,
            verifications_fingerprint=verifications_fingerprint,
            reporting_fingerprint=reporting_fingerprint,
            platform_tenant_id=platform_tenant_id,
            evidence=evidence,
            evidence_complete=evidence_complete,
        )

        return operation_workflow_readiness_snapshot_entry
