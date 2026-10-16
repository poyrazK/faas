from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_applicability_status import (
    OperationWorkflowPlaybookApplicabilityStatus,
    check_operation_workflow_playbook_applicability_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybookApplicability")


@_attrs_define
class OperationWorkflowPlaybookApplicability:
    """OperationWorkflowPlaybookApplicability. Ephemeral applicability of a durable pinned checklist against current exact-
    case evidence. No progress, review history, readiness or outcome is mutated.

    """

    status: OperationWorkflowPlaybookApplicabilityStatus
    """OperationWorkflowPlaybookApplicability.status. Current advisory disposition; absent triggers and terminal
    reports are not business success."""
    summary: str
    """OperationWorkflowPlaybookApplicability.summary. Evidence interpretation, including uncertainty and authority
    boundaries."""
    suggested_action: str
    """OperationWorkflowPlaybookApplicability.suggested_action. Human review suggestion; never execution permission
    or an automatic checklist closure."""
    checked_at: datetime.datetime
    """OperationWorkflowPlaybookApplicability.checked_at. Shared server observation clock for the current case
    checklists; sources are independently read."""
    definition_changed: bool
    """OperationWorkflowPlaybookApplicability.definition_changed. An observed reporting definition differs from the
    pinned original. False does not establish availability; inspect definition_id and status."""
    trigger_matched: bool
    """OperationWorkflowPlaybookApplicability.trigger_matched. The unchanged pinned rule positively matches current
    retained evidence. False may mean unevaluated or incomplete evidence; inspect status."""
    evidence_complete: bool
    """OperationWorkflowPlaybookApplicability.evidence_complete. Relevant trigger coverage is complete for this
    retained evaluation. Does not validate existing checklist attachments or infer business correctness."""
    remaining_item_count: int
    """OperationWorkflowPlaybookApplicability.remaining_item_count. Count of frozen checklist items not human-
    reviewed as completed; independent of current trigger or business outcome."""
    current_contract_version: int | Unset = UNSET
    """OperationWorkflowPlaybookApplicability.current_contract_version. Current projected workflow contract
    version, if a report exists; status determines comparability."""
    state_revision: int | Unset = UNSET
    """OperationWorkflowPlaybookApplicability.state_revision. Observed application state revision, when available;
    stale reports remain explicitly unavailable."""
    reporting_operation_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookApplicability.reporting_operation_id. Current state-report operation, when
    available; distinct from the frozen original reporting operation."""
    definition_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookApplicability.definition_id. Exact current reporting definition validated under
    account, application, customer, environment and subject fences, if available."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        summary = self.summary

        suggested_action = self.suggested_action

        checked_at = self.checked_at.isoformat()

        definition_changed = self.definition_changed

        trigger_matched = self.trigger_matched

        evidence_complete = self.evidence_complete

        remaining_item_count = self.remaining_item_count

        current_contract_version = self.current_contract_version

        state_revision = self.state_revision

        reporting_operation_id: str | Unset = UNSET
        if not isinstance(self.reporting_operation_id, Unset):
            reporting_operation_id = str(self.reporting_operation_id)

        definition_id: str | Unset = UNSET
        if not isinstance(self.definition_id, Unset):
            definition_id = str(self.definition_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "summary": summary,
                "suggested_action": suggested_action,
                "checked_at": checked_at,
                "definition_changed": definition_changed,
                "trigger_matched": trigger_matched,
                "evidence_complete": evidence_complete,
                "remaining_item_count": remaining_item_count,
            }
        )
        if current_contract_version is not UNSET:
            field_dict["current_contract_version"] = current_contract_version
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision
        if reporting_operation_id is not UNSET:
            field_dict["reporting_operation_id"] = reporting_operation_id
        if definition_id is not UNSET:
            field_dict["definition_id"] = definition_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_operation_workflow_playbook_applicability_status(d.pop("status"))

        summary = d.pop("summary")

        suggested_action = d.pop("suggested_action")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        definition_changed = d.pop("definition_changed")

        trigger_matched = d.pop("trigger_matched")

        evidence_complete = d.pop("evidence_complete")

        remaining_item_count = d.pop("remaining_item_count")

        current_contract_version = d.pop("current_contract_version", UNSET)

        state_revision = d.pop("state_revision", UNSET)

        _reporting_operation_id = d.pop("reporting_operation_id", UNSET)
        reporting_operation_id: UUID | Unset
        if isinstance(_reporting_operation_id, Unset):
            reporting_operation_id = UNSET
        else:
            reporting_operation_id = UUID(_reporting_operation_id)

        _definition_id = d.pop("definition_id", UNSET)
        definition_id: UUID | Unset
        if isinstance(_definition_id, Unset):
            definition_id = UNSET
        else:
            definition_id = UUID(_definition_id)

        operation_workflow_playbook_applicability = cls(
            status=status,
            summary=summary,
            suggested_action=suggested_action,
            checked_at=checked_at,
            definition_changed=definition_changed,
            trigger_matched=trigger_matched,
            evidence_complete=evidence_complete,
            remaining_item_count=remaining_item_count,
            current_contract_version=current_contract_version,
            state_revision=state_revision,
            reporting_operation_id=reporting_operation_id,
            definition_id=definition_id,
        )

        return operation_workflow_playbook_applicability
