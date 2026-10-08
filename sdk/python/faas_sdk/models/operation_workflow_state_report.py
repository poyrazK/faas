from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_state_report_deadline_at_type_1 import (
    OperationWorkflowStateReportDeadlineAtType1,
    check_operation_workflow_state_report_deadline_at_type_1,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_blocker import OperationWorkflowBlocker
    from ..models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution
    from ..models.operation_workflow_dependency import OperationWorkflowDependency
    from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone


T = TypeVar("T", bound="OperationWorkflowStateReport")


@_attrs_define
class OperationWorkflowStateReport:
    """Idempotent app-reported state update already committed with the business write. Revision is assigned transactionally
    by the application SDK. Contract version is filled from the pinned definition when omitted.

    """

    id: UUID
    workflow: str
    instance_id: str
    state: str
    revision: int
    occurred_at: datetime.datetime
    depends_on: list[OperationWorkflowDependency] | Unset = UNSET
    """Full replacement snapshot of direct prerequisite references."""
    dependencies_only: bool | Unset = UNSET
    """Same-state metadata update mutually exclusive with other metadata-only flags."""
    outcome_code: str | Unset = UNSET
    """Explicit application-defined business result for a declared terminal state."""
    outcome_description: str | Unset = UNSET
    """Public UTF-8 description limited to 512 bytes without control characters. Required when outcome_code is
    supplied."""
    outcome_only: bool | Unset = UNSET
    """Same-state terminal outcome report. Requires from_state equal to state and no milestone evidence. Mutually
    exclusive with deadline_only and blockers_only. SDKs preserve current blockers and deadline."""
    deadline_at: datetime.datetime | OperationWorkflowStateReportDeadlineAtType1 | Unset = UNSET
    """Optional application-reported due time. Omitted or empty in a report clears the deadline. Transactional SDKs
    inherit it from their counter unless explicitly updated."""
    deadline_only: bool | Unset = UNSET
    """Same-state deadline snapshot update. Requires from_state equal to state and no milestone evidence. Mutually
    exclusive with blockers_only. SDKs preserve current blockers."""
    blocker_resolutions: list[OperationWorkflowBlockerResolution] | Unset = UNSET
    blockers: list[OperationWorkflowBlocker] | Unset = UNSET
    blockers_only: bool | Unset = UNSET
    """Same-state blocker replacement. Requires from_state equal to state and no milestone evidence; not a business
    transition."""
    from_state: str | Unset = UNSET
    """Current app database state before the requested declared transition. Required when the pinned workflow
    declares transitions."""
    contract_version: int | Unset = UNSET
    """Optional version precondition; the server fills this from the immutable Operation definition."""
    evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
    """Facts committed in the same application transaction as this transition."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        revision = self.revision

        occurred_at = self.occurred_at.isoformat()

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

        deadline_at: str | Unset
        if isinstance(self.deadline_at, Unset):
            deadline_at = UNSET
        elif isinstance(self.deadline_at, datetime.datetime):
            deadline_at = self.deadline_at.isoformat()
        else:
            deadline_at = self.deadline_at

        deadline_only = self.deadline_only

        blocker_resolutions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blocker_resolutions, Unset):
            blocker_resolutions = []
            for blocker_resolutions_item_data in self.blocker_resolutions:
                blocker_resolutions_item = blocker_resolutions_item_data.to_dict()
                blocker_resolutions.append(blocker_resolutions_item)

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        blockers_only = self.blockers_only

        from_state = self.from_state

        contract_version = self.contract_version

        evidence_milestones: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence_milestones, Unset):
            evidence_milestones = []
            for evidence_milestones_item_data in self.evidence_milestones:
                evidence_milestones_item = evidence_milestones_item_data.to_dict()
                evidence_milestones.append(evidence_milestones_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "revision": revision,
                "occurred_at": occurred_at,
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
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if deadline_only is not UNSET:
            field_dict["deadline_only"] = deadline_only
        if blocker_resolutions is not UNSET:
            field_dict["blocker_resolutions"] = blocker_resolutions
        if blockers is not UNSET:
            field_dict["blockers"] = blockers
        if blockers_only is not UNSET:
            field_dict["blockers_only"] = blockers_only
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if contract_version is not UNSET:
            field_dict["contract_version"] = contract_version
        if evidence_milestones is not UNSET:
            field_dict["evidence_milestones"] = evidence_milestones

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_blocker import OperationWorkflowBlocker
        from ..models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution
        from ..models.operation_workflow_dependency import OperationWorkflowDependency
        from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        revision = d.pop("revision")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

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

        def _parse_deadline_at(data: object) -> datetime.datetime | OperationWorkflowStateReportDeadlineAtType1 | Unset:
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deadline_at_type_0 = datetime.datetime.fromisoformat(data)

                return deadline_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, str):
                raise TypeError()
            deadline_at_type_1 = check_operation_workflow_state_report_deadline_at_type_1(data)

            return deadline_at_type_1

        deadline_at = _parse_deadline_at(d.pop("deadline_at", UNSET))

        deadline_only = d.pop("deadline_only", UNSET)

        _blocker_resolutions = d.pop("blocker_resolutions", UNSET)
        blocker_resolutions: list[OperationWorkflowBlockerResolution] | Unset = UNSET
        if _blocker_resolutions is not UNSET:
            blocker_resolutions = []
            for blocker_resolutions_item_data in _blocker_resolutions:
                blocker_resolutions_item = OperationWorkflowBlockerResolution.from_dict(blocker_resolutions_item_data)

                blocker_resolutions.append(blocker_resolutions_item)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[OperationWorkflowBlocker] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = OperationWorkflowBlocker.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        blockers_only = d.pop("blockers_only", UNSET)

        from_state = d.pop("from_state", UNSET)

        contract_version = d.pop("contract_version", UNSET)

        _evidence_milestones = d.pop("evidence_milestones", UNSET)
        evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
        if _evidence_milestones is not UNSET:
            evidence_milestones = []
            for evidence_milestones_item_data in _evidence_milestones:
                evidence_milestones_item = OperationWorkflowEvidenceMilestone.from_dict(evidence_milestones_item_data)

                evidence_milestones.append(evidence_milestones_item)

        operation_workflow_state_report = cls(
            id=id,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            revision=revision,
            occurred_at=occurred_at,
            depends_on=depends_on,
            dependencies_only=dependencies_only,
            outcome_code=outcome_code,
            outcome_description=outcome_description,
            outcome_only=outcome_only,
            deadline_at=deadline_at,
            deadline_only=deadline_only,
            blocker_resolutions=blocker_resolutions,
            blockers=blockers,
            blockers_only=blockers_only,
            from_state=from_state,
            contract_version=contract_version,
            evidence_milestones=evidence_milestones,
        )

        return operation_workflow_state_report
