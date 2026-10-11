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
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification


T = TypeVar("T", bound="OperationWorkflowStateHistoryEntry")


@_attrs_define
class OperationWorkflowStateHistoryEntry:
    """One retained app-reported state update. Pages are ordered by revision, then stable publication and report
    identifiers.

    """

    id: UUID
    operation_id: UUID
    workflow: str
    instance_id: str
    state: str
    revision: int
    contract_version: int
    occurred_at: datetime.datetime
    published_at: datetime.datetime
    resolution_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
    """Current retained-evidence status for verification obligations in this history report."""
    depends_on: list[OperationWorkflowDependency] | Unset = UNSET
    """At this retained historical revision, full replacement snapshot of direct prerequisite references."""
    dependencies_only: bool | Unset = UNSET
    """At this retained historical revision, same-state metadata update mutually exclusive with other metadata-only
    flags."""
    outcome_code: str | Unset = UNSET
    """At this retained historical revision, explicit application-defined business result for a declared terminal
    state."""
    outcome_description: str | Unset = UNSET
    """At this retained historical revision, public UTF-8 description limited to 512 bytes without control
    characters. Required when outcome_code is supplied."""
    outcome_only: bool | Unset = UNSET
    """At this retained historical revision, same-state terminal outcome report. Requires from_state equal to state
    and no milestone evidence. Mutually exclusive with deadline_only and blockers_only. SDKs preserve current
    blockers and deadline."""
    deadline_at: datetime.datetime | Unset = UNSET
    """At this retained historical revision, optional application-reported due time. Omitted or empty in a report
    clears the deadline. Transactional SDKs inherit it from their counter unless explicitly updated."""
    deadline_only: bool | Unset = UNSET
    """At this retained historical revision, same-state deadline snapshot update. Requires from_state equal to
    state and no milestone evidence. Mutually exclusive with blockers_only. SDKs preserve current blockers."""
    blocker_resolutions: list[OperationWorkflowBlockerResolution] | Unset = UNSET
    blockers: list[OperationWorkflowBlocker] | Unset = UNSET
    blockers_only: bool | Unset = UNSET
    """At this retained historical revision, same-state blocker replacement. Requires from_state equal to state and
    no milestone evidence; not a business transition."""
    from_state: str | Unset = UNSET
    """App state immediately before this retained revision, when the transition was declared."""
    evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    """Account-owner tenant identifier attached to this report in operator feeds."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        operation_id = str(self.operation_id)

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        revision = self.revision

        contract_version = self.contract_version

        occurred_at = self.occurred_at.isoformat()

        published_at = self.published_at.isoformat()

        resolution_verifications: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.resolution_verifications, Unset):
            resolution_verifications = []
            for resolution_verifications_item_data in self.resolution_verifications:
                resolution_verifications_item = resolution_verifications_item_data.to_dict()
                resolution_verifications.append(resolution_verifications_item)

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

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        blockers_only = self.blockers_only

        from_state = self.from_state

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
                "id": id,
                "operation_id": operation_id,
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "revision": revision,
                "contract_version": contract_version,
                "occurred_at": occurred_at,
                "published_at": published_at,
            }
        )
        if resolution_verifications is not UNSET:
            field_dict["resolution_verifications"] = resolution_verifications
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
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        operation_id = UUID(d.pop("operation_id"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        revision = d.pop("revision")

        contract_version = d.pop("contract_version")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        published_at = datetime.datetime.fromisoformat(d.pop("published_at"))

        _resolution_verifications = d.pop("resolution_verifications", UNSET)
        resolution_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
        if _resolution_verifications is not UNSET:
            resolution_verifications = []
            for resolution_verifications_item_data in _resolution_verifications:
                resolution_verifications_item = OperationWorkflowResolutionVerification.from_dict(
                    resolution_verifications_item_data
                )

                resolution_verifications.append(resolution_verifications_item)

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

        _blockers = d.pop("blockers", UNSET)
        blockers: list[OperationWorkflowBlocker] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = OperationWorkflowBlocker.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        blockers_only = d.pop("blockers_only", UNSET)

        from_state = d.pop("from_state", UNSET)

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

        operation_workflow_state_history_entry = cls(
            id=id,
            operation_id=operation_id,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            revision=revision,
            contract_version=contract_version,
            occurred_at=occurred_at,
            published_at=published_at,
            resolution_verifications=resolution_verifications,
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
            evidence_milestones=evidence_milestones,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_state_history_entry
