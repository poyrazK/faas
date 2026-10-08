from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone


T = TypeVar("T", bound="OperationWorkflowStateReportResponse")


@_attrs_define
class OperationWorkflowStateReportResponse:
    """Acknowledgement returned after the platform records an app-reported workflow-state update."""

    id: UUID
    operation_id: UUID
    workflow: str
    instance_id: str
    state: str
    revision: int
    contract_version: int
    from_state: str | Unset = UNSET
    """Previous app state when a declared transition was reported."""
    evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        operation_id = str(self.operation_id)

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        revision = self.revision

        contract_version = self.contract_version

        from_state = self.from_state

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
                "operation_id": operation_id,
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "revision": revision,
                "contract_version": contract_version,
            }
        )
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if evidence_milestones is not UNSET:
            field_dict["evidence_milestones"] = evidence_milestones

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        operation_id = UUID(d.pop("operation_id"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        revision = d.pop("revision")

        contract_version = d.pop("contract_version")

        from_state = d.pop("from_state", UNSET)

        _evidence_milestones = d.pop("evidence_milestones", UNSET)
        evidence_milestones: list[OperationWorkflowEvidenceMilestone] | Unset = UNSET
        if _evidence_milestones is not UNSET:
            evidence_milestones = []
            for evidence_milestones_item_data in _evidence_milestones:
                evidence_milestones_item = OperationWorkflowEvidenceMilestone.from_dict(evidence_milestones_item_data)

                evidence_milestones.append(evidence_milestones_item)

        operation_workflow_state_report_response = cls(
            id=id,
            operation_id=operation_id,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            revision=revision,
            contract_version=contract_version,
            from_state=from_state,
            evidence_milestones=evidence_milestones,
        )

        return operation_workflow_state_report_response
