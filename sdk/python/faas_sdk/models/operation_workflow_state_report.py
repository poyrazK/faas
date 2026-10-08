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
    from_state: str | Unset = UNSET
    """Current app database state before the requested declared transition. Required when the pinned workflow
    declares transitions."""
    contract_version: int | Unset = UNSET
    """Optional version precondition; the server fills this from the immutable Operation definition."""
    evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
    """Facts committed in the same application transaction as this transition."""

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

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        revision = self.revision

        occurred_at = self.occurred_at.isoformat()

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
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if contract_version is not UNSET:
            field_dict["contract_version"] = contract_version
        if evidence_milestones is not UNSET:
            field_dict["evidence_milestones"] = evidence_milestones

        if not isinstance(self.blockers, Unset):
            field_dict["blockers"] = [b.to_dict() for b in self.blockers]
        if self.depends_on is not UNSET: field_dict["depends_on"]=[d.to_dict() for d in self.depends_on]
        if self.dependencies_only is not UNSET: field_dict["dependencies_only"]=self.dependencies_only
        if self.outcome_code is not UNSET: field_dict["outcome_code"] = self.outcome_code
        if self.outcome_description is not UNSET: field_dict["outcome_description"] = self.outcome_description
        if self.outcome_only is not UNSET: field_dict["outcome_only"] = self.outcome_only
        if self.deadline_at is not UNSET: field_dict["deadline_at"] = self.deadline_at
        if self.deadline_only is not UNSET: field_dict["deadline_only"] = self.deadline_only
        if self.blockers_only is not UNSET:
            field_dict["blockers_only"] = self.blockers_only
        if not isinstance(self.blocker_resolutions, Unset):
            field_dict["blocker_resolutions"] = [v.to_dict() for v in self.blocker_resolutions]
        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        revision = d.pop("revision")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

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
            blocker_resolutions=[OperationWorkflowBlockerResolution.from_dict(v) for v in d.pop("blocker_resolutions")] if "blocker_resolutions" in d else UNSET,
            blockers=[OperationWorkflowBlocker.from_dict(b) for b in d.pop("blockers")] if "blockers" in d else UNSET,
            depends_on=[OperationWorkflowDependency.from_dict(v) for v in d.pop("depends_on")] if "depends_on" in d else UNSET,
            dependencies_only=d.pop("dependencies_only", UNSET),
            outcome_code=d.pop("outcome_code", UNSET),
            outcome_description=d.pop("outcome_description", UNSET),
            outcome_only=d.pop("outcome_only", UNSET),
            deadline_at=d.pop("deadline_at", UNSET),
            deadline_only=d.pop("deadline_only", UNSET),
            blockers_only=d.pop("blockers_only", UNSET),
            id=id,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            revision=revision,
            occurred_at=occurred_at,
            from_state=from_state,
            contract_version=contract_version,
            evidence_milestones=evidence_milestones,
        )

        return operation_workflow_state_report
