from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_blocker import OperationWorkflowBlocker
    from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification


T = TypeVar("T", bound="OperationWorkflowReadinessQueueEntry")


@_attrs_define
class OperationWorkflowReadinessQueueEntry:
    """One declared current-state transition evaluated without planned evidence; reporting is advisory."""

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    """Declared workflow name."""
    instance_id: str
    """Application workflow instance identifier."""
    operation_id: UUID
    """Current reporting Operation identifier."""
    state_revision: int
    """Evaluated current state revision."""
    contract_version: int
    """Evaluated pinned workflow contract version."""
    transition: OperationWorkflowInstanceTransition
    """One allowed state edge from the selected contract version, bound to the Operation that can report it.
    Required milestones must be committed with the transition in the same application transaction. This declaration
    does not establish that the edge is currently valid for the application's business row or authorized for the
    caller."""
    evaluated_at: datetime.datetime
    """Shared reporting and retained-evidence evaluation time for this workflow."""
    ready: bool
    """Whether declared requirements are met without planned evidence."""
    reasons: list[str]
    """Unmet declared requirement codes."""
    advisories: list[str]
    """Reporting or verification context that does not change readiness."""
    owners: list[str]
    """Sorted unique blocker and pending verification owners for the target Operation."""
    unassigned: bool
    """Whether a matching blocker or pending verification has no owner."""
    blockers: list[OperationWorkflowBlocker]
    """Application-reported blockers for this transition Operation."""
    missing_milestones: list[str]
    """Required planned milestone names absent from the empty plan."""
    missing_dependency_workflows: list[str]
    """Declared prerequisite workflows without a reported dependency reference."""
    pending_verifications_truncated: bool
    """More pending evidence obligations exist than the queue preview contains."""
    pending_verification_count: int
    """Exact pending obligation count before previews."""
    reporting_status: str
    """Current-state reporting coverage status; unknown coverage does not invent missing evidence."""
    missing_report_count: int
    """Known missing condition reports when reporting coverage is comparable."""
    overdue_report_count: int
    """Known overdue condition reports when reporting coverage is comparable."""
    platform_tenant_id: UUID | Unset = UNSET
    """Customer identity present only on the account surface."""
    pending_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
    """Canonical preview of pending evidence obligations for this transition Operation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        operation_id = str(self.operation_id)

        state_revision = self.state_revision

        contract_version = self.contract_version

        transition = self.transition.to_dict()

        evaluated_at = self.evaluated_at.isoformat()

        ready = self.ready

        reasons = self.reasons

        advisories = self.advisories

        owners = self.owners

        unassigned = self.unassigned

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        missing_milestones = self.missing_milestones

        missing_dependency_workflows = self.missing_dependency_workflows

        pending_verifications_truncated = self.pending_verifications_truncated

        pending_verification_count = self.pending_verification_count

        reporting_status = self.reporting_status

        missing_report_count = self.missing_report_count

        overdue_report_count = self.overdue_report_count

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        pending_verifications: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.pending_verifications, Unset):
            pending_verifications = []
            for pending_verifications_item_data in self.pending_verifications:
                pending_verifications_item = pending_verifications_item_data.to_dict()
                pending_verifications.append(pending_verifications_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "operation_id": operation_id,
                "state_revision": state_revision,
                "contract_version": contract_version,
                "transition": transition,
                "evaluated_at": evaluated_at,
                "ready": ready,
                "reasons": reasons,
                "advisories": advisories,
                "owners": owners,
                "unassigned": unassigned,
                "blockers": blockers,
                "missing_milestones": missing_milestones,
                "missing_dependency_workflows": missing_dependency_workflows,
                "pending_verifications_truncated": pending_verifications_truncated,
                "pending_verification_count": pending_verification_count,
                "reporting_status": reporting_status,
                "missing_report_count": missing_report_count,
                "overdue_report_count": overdue_report_count,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if pending_verifications is not UNSET:
            field_dict["pending_verifications"] = pending_verifications

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_blocker import OperationWorkflowBlocker
        from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        operation_id = UUID(d.pop("operation_id"))

        state_revision = d.pop("state_revision")

        contract_version = d.pop("contract_version")

        transition = OperationWorkflowInstanceTransition.from_dict(d.pop("transition"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        ready = d.pop("ready")

        reasons = cast(list[str], d.pop("reasons"))

        advisories = cast(list[str], d.pop("advisories"))

        owners = cast(list[str], d.pop("owners"))

        unassigned = d.pop("unassigned")

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = OperationWorkflowBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        missing_milestones = cast(list[str], d.pop("missing_milestones"))

        missing_dependency_workflows = cast(list[str], d.pop("missing_dependency_workflows"))

        pending_verifications_truncated = d.pop("pending_verifications_truncated")

        pending_verification_count = d.pop("pending_verification_count")

        reporting_status = d.pop("reporting_status")

        missing_report_count = d.pop("missing_report_count")

        overdue_report_count = d.pop("overdue_report_count")

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _pending_verifications = d.pop("pending_verifications", UNSET)
        pending_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
        if _pending_verifications is not UNSET:
            pending_verifications = []
            for pending_verifications_item_data in _pending_verifications:
                pending_verifications_item = OperationWorkflowResolutionVerification.from_dict(
                    pending_verifications_item_data
                )

                pending_verifications.append(pending_verifications_item)

        operation_workflow_readiness_queue_entry = cls(
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            operation_id=operation_id,
            state_revision=state_revision,
            contract_version=contract_version,
            transition=transition,
            evaluated_at=evaluated_at,
            ready=ready,
            reasons=reasons,
            advisories=advisories,
            owners=owners,
            unassigned=unassigned,
            blockers=blockers,
            missing_milestones=missing_milestones,
            missing_dependency_workflows=missing_dependency_workflows,
            pending_verifications_truncated=pending_verifications_truncated,
            pending_verification_count=pending_verification_count,
            reporting_status=reporting_status,
            missing_report_count=missing_report_count,
            overdue_report_count=overdue_report_count,
            platform_tenant_id=platform_tenant_id,
            pending_verifications=pending_verifications,
        )

        operation_workflow_readiness_queue_entry.additional_properties = d
        return operation_workflow_readiness_queue_entry

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
