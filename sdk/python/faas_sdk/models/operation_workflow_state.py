from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset
from .operation_workflow_dependency import OperationWorkflowDependency
from .operation_workflow_blocker import OperationWorkflowBlocker
from .operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution

if TYPE_CHECKING:
    from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone


T = TypeVar("T", bound="OperationWorkflowState")


@_attrs_define
class OperationWorkflowState:
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""

    workflow: str
    instance_id: str
    state: str
    terminal: bool
    """True when the state is listed in terminal_states on the pinned workflow definition that reported it."""
    stale: bool
    """True when the app-reported occurrence time plus the pinned state_stale_after threshold is at or before the
    read time."""
    occurred_at: datetime.datetime
    """App-reported time when the current state became true; used for stale-state age."""
    revision: int
    contract_version: int
    updated_at: datetime.datetime
    stale_after_seconds: int | Unset = UNSET
    """App-declared age threshold for the current state when one is configured."""
    evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    """Included only for account operator feeds."""

    report_id: UUID | Unset = UNSET
    operation_id: UUID | Unset = UNSET
    blocker_resolutions: list[OperationWorkflowBlockerResolution] | Unset = UNSET
    blockers: list[OperationWorkflowBlocker] | Unset = UNSET
    depends_on: list[OperationWorkflowDependency] | Unset = UNSET
    dependencies_only: bool | Unset = UNSET
    outcome_code: str | Unset = UNSET
    outcome_description: str | Unset = UNSET
    outcome_only: bool | Unset = UNSET
    deadline_at: str | Unset = UNSET
    deadline_only: bool | Unset = UNSET
    blockers_only: bool | Unset = UNSET
    overdue: bool | Unset = UNSET
    overdue_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        terminal = self.terminal

        stale = self.stale

        occurred_at = self.occurred_at.isoformat()

        revision = self.revision

        contract_version = self.contract_version

        updated_at = self.updated_at.isoformat()

        stale_after_seconds = self.stale_after_seconds

        evidence_milestones: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence_milestones, Unset):
            evidence_milestones = []
            for evidence_milestones_item_data in self.evidence_milestones:
                evidence_milestones_item = evidence_milestones_item_data.to_dict()
                evidence_milestones.append(evidence_milestones_item)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "terminal": terminal,
                "stale": stale,
                "occurred_at": occurred_at,
                "revision": revision,
                "contract_version": contract_version,
                "updated_at": updated_at,
            }
        )
        if stale_after_seconds is not UNSET:
            field_dict["stale_after_seconds"] = stale_after_seconds
        if evidence_milestones is not UNSET:
            field_dict["evidence_milestones"] = evidence_milestones
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        if not isinstance(self.blockers, Unset):
            field_dict["blockers"] = [b.to_dict() for b in self.blockers]
        if self.depends_on is not UNSET: field_dict["depends_on"]=[d.to_dict() for d in self.depends_on]
        if self.dependencies_only is not UNSET: field_dict["dependencies_only"]=self.dependencies_only
        if self.outcome_code is not UNSET: field_dict["outcome_code"] = self.outcome_code
        if self.outcome_description is not UNSET: field_dict["outcome_description"] = self.outcome_description
        if self.outcome_only is not UNSET: field_dict["outcome_only"] = self.outcome_only
        if self.deadline_at is not UNSET: field_dict["deadline_at"] = self.deadline_at
        if self.deadline_only is not UNSET: field_dict["deadline_only"] = self.deadline_only
        if self.overdue is not UNSET: field_dict["overdue"] = self.overdue
        if self.overdue_seconds is not UNSET: field_dict["overdue_seconds"] = self.overdue_seconds
        if self.blockers_only is not UNSET:
            field_dict["blockers_only"] = self.blockers_only
        if not isinstance(self.blocker_resolutions, Unset):
            field_dict["blocker_resolutions"] = [v.to_dict() for v in self.blocker_resolutions]
        if not isinstance(self.report_id, Unset):
            field_dict["report_id"] = str(self.report_id)
        if not isinstance(self.operation_id, Unset):
            field_dict["operation_id"] = str(self.operation_id)
        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone

        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        terminal = d.pop("terminal")

        stale = d.pop("stale")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        revision = d.pop("revision")

        contract_version = d.pop("contract_version")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        stale_after_seconds = d.pop("stale_after_seconds", UNSET)

        _evidence_milestones = d.pop("evidence_milestones", UNSET)
        evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
        if _evidence_milestones is not UNSET:
            evidence_milestones = []
            for evidence_milestones_item_data in _evidence_milestones:
                evidence_milestones_item = OperationWorkflowEvidenceMilestone.from_dict(evidence_milestones_item_data)

                evidence_milestones.append(evidence_milestones_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_state = cls(
            report_id=UUID(d.pop("report_id")) if "report_id" in d else UNSET,
            operation_id=UUID(d.pop("operation_id")) if "operation_id" in d else UNSET,
            blocker_resolutions=[OperationWorkflowBlockerResolution.from_dict(v) for v in d.pop("blocker_resolutions")] if "blocker_resolutions" in d else UNSET,
            blockers=[OperationWorkflowBlocker.from_dict(b) for b in d.pop("blockers")] if "blockers" in d else UNSET,
            depends_on=[OperationWorkflowDependency.from_dict(v) for v in d.pop("depends_on")] if "depends_on" in d else UNSET,
            dependencies_only=d.pop("dependencies_only", UNSET),
            outcome_code=d.pop("outcome_code", UNSET),
            outcome_description=d.pop("outcome_description", UNSET),
            outcome_only=d.pop("outcome_only", UNSET),
            deadline_at=d.pop("deadline_at", UNSET),
            deadline_only=d.pop("deadline_only", UNSET),
            overdue=d.pop("overdue", UNSET),
            overdue_seconds=d.pop("overdue_seconds", UNSET),
            blockers_only=d.pop("blockers_only", UNSET),
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            terminal=terminal,
            stale=stale,
            occurred_at=occurred_at,
            revision=revision,
            contract_version=contract_version,
            updated_at=updated_at,
            stale_after_seconds=stale_after_seconds,
            evidence_milestones=evidence_milestones,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_state
