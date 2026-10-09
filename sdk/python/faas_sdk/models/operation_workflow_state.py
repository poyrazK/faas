from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_blocker import OperationWorkflowBlocker
    from ..models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution
    from ..models.operation_workflow_dependency import OperationWorkflowDependency
    from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone


T = TypeVar("T", bound="OperationWorkflowState")


@_attrs_define
class OperationWorkflowState:
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""

    overdue: bool
    """True for an active workflow at or after its application-reported deadline."""
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
    depends_on: list[OperationWorkflowDependency] | Unset = UNSET
    """In the latest retained state, full replacement snapshot of direct prerequisite references."""
    dependencies_only: bool | Unset = UNSET
    """In the latest retained state, same-state metadata update mutually exclusive with other metadata-only flags."""
    outcome_code: str | Unset = UNSET
    """In the latest retained state, explicit application-defined business result for a declared terminal state."""
    outcome_description: str | Unset = UNSET
    """In the latest retained state, public UTF-8 description limited to 512 bytes without control characters.
    Required when outcome_code is supplied."""
    outcome_only: bool | Unset = UNSET
    """In the latest retained state, same-state terminal outcome report. Requires from_state equal to state and no
    milestone evidence. Mutually exclusive with deadline_only and blockers_only. SDKs preserve current blockers and
    deadline."""
    overdue_seconds: int | Unset = UNSET
    """Whole elapsed seconds since the missed due time."""
    deadline_at: datetime.datetime | Unset = UNSET
    """In the latest retained state, optional application-reported due time. Omitted or empty in a report clears
    the deadline. Transactional SDKs inherit it from their counter unless explicitly updated."""
    deadline_only: bool | Unset = UNSET
    """In the latest retained state, same-state deadline snapshot update. Requires from_state equal to state and no
    milestone evidence. Mutually exclusive with blockers_only. SDKs preserve current blockers."""
    blocker_resolutions: list[OperationWorkflowBlockerResolution] | Unset = UNSET
    report_id: UUID | Unset = UNSET
    """Identity of the latest reported snapshot."""
    operation_id: UUID | Unset = UNSET
    """Operation that published the latest snapshot."""
    blockers: list[OperationWorkflowBlocker] | Unset = UNSET
    blockers_only: bool | Unset = UNSET
    """In the latest retained state, same-state blocker replacement. Requires from_state equal to state and no
    milestone evidence; not a business transition."""
    stale_after_seconds: int | Unset = UNSET
    """App-declared age threshold for the current state when one is configured."""
    evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    """Included only for account operator feeds."""

    def to_dict(self) -> dict[str, Any]:
        overdue = self.overdue

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        terminal = self.terminal

        stale = self.stale

        occurred_at = self.occurred_at.isoformat()

        revision = self.revision

        contract_version = self.contract_version

        updated_at = self.updated_at.isoformat()

        depends_on: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.depends_on, Unset):
            depends_on = []
            for depends_on_item_data in self.depends_on:
                depends_on_item = depends_on_item_data.to_dict()
                depends_on.append(depends_on_item)

        dependencies_only = self.dependencies_only

        outcome_code = self.outcome_code

        outcome_description = self.outcome_description

        outcome_only = self.outcome_only

        overdue_seconds = self.overdue_seconds

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        deadline_only = self.deadline_only

        blocker_resolutions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blocker_resolutions, Unset):
            blocker_resolutions = []
            for blocker_resolutions_item_data in self.blocker_resolutions:
                blocker_resolutions_item = blocker_resolutions_item_data.to_dict()
                blocker_resolutions.append(blocker_resolutions_item)

        report_id: str | Unset = UNSET
        if not isinstance(self.report_id, Unset):
            report_id = str(self.report_id)

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        blockers_only = self.blockers_only

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
                "overdue": overdue,
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
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on
        if dependencies_only is not UNSET:
            field_dict["dependencies_only"] = dependencies_only
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code
        if outcome_description is not UNSET:
            field_dict["outcome_description"] = outcome_description
        if outcome_only is not UNSET:
            field_dict["outcome_only"] = outcome_only
        if overdue_seconds is not UNSET:
            field_dict["overdue_seconds"] = overdue_seconds
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if deadline_only is not UNSET:
            field_dict["deadline_only"] = deadline_only
        if blocker_resolutions is not UNSET:
            field_dict["blocker_resolutions"] = blocker_resolutions
        if report_id is not UNSET:
            field_dict["report_id"] = report_id
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id
        if blockers is not UNSET:
            field_dict["blockers"] = blockers
        if blockers_only is not UNSET:
            field_dict["blockers_only"] = blockers_only
        if stale_after_seconds is not UNSET:
            field_dict["stale_after_seconds"] = stale_after_seconds
        if evidence_milestones is not UNSET:
            field_dict["evidence_milestones"] = evidence_milestones
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_blocker import OperationWorkflowBlocker
        from ..models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution
        from ..models.operation_workflow_dependency import OperationWorkflowDependency
        from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone

        d = dict(src_dict)
        overdue = d.pop("overdue")

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        terminal = d.pop("terminal")

        stale = d.pop("stale")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        revision = d.pop("revision")

        contract_version = d.pop("contract_version")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _depends_on = d.pop("depends_on", UNSET)
        depends_on: list[OperationWorkflowDependency] | Unset = UNSET
        if _depends_on is not UNSET:
            depends_on = []
            for depends_on_item_data in _depends_on:
                depends_on_item = OperationWorkflowDependency.from_dict(depends_on_item_data)

                depends_on.append(depends_on_item)

        dependencies_only = d.pop("dependencies_only", UNSET)

        outcome_code = d.pop("outcome_code", UNSET)

        outcome_description = d.pop("outcome_description", UNSET)

        outcome_only = d.pop("outcome_only", UNSET)

        overdue_seconds = d.pop("overdue_seconds", UNSET)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        deadline_only = d.pop("deadline_only", UNSET)

        _blocker_resolutions = d.pop("blocker_resolutions", UNSET)
        blocker_resolutions: list[OperationWorkflowBlockerResolution] | Unset = UNSET
        if _blocker_resolutions is not UNSET:
            blocker_resolutions = []
            for blocker_resolutions_item_data in _blocker_resolutions:
                blocker_resolutions_item = OperationWorkflowBlockerResolution.from_dict(blocker_resolutions_item_data)

                blocker_resolutions.append(blocker_resolutions_item)

        _report_id = d.pop("report_id", UNSET)
        report_id: UUID | Unset
        if isinstance(_report_id, Unset):
            report_id = UNSET
        else:
            report_id = UUID(_report_id)

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[OperationWorkflowBlocker] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = OperationWorkflowBlocker.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        blockers_only = d.pop("blockers_only", UNSET)

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
            overdue=overdue,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            terminal=terminal,
            stale=stale,
            occurred_at=occurred_at,
            revision=revision,
            contract_version=contract_version,
            updated_at=updated_at,
            depends_on=depends_on,
            dependencies_only=dependencies_only,
            outcome_code=outcome_code,
            outcome_description=outcome_description,
            outcome_only=outcome_only,
            overdue_seconds=overdue_seconds,
            deadline_at=deadline_at,
            deadline_only=deadline_only,
            blocker_resolutions=blocker_resolutions,
            report_id=report_id,
            operation_id=operation_id,
            blockers=blockers,
            blockers_only=blockers_only,
            stale_after_seconds=stale_after_seconds,
            evidence_milestones=evidence_milestones,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_state
