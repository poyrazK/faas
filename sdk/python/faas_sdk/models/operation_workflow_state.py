from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

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
