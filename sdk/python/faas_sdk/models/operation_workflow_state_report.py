from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

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
