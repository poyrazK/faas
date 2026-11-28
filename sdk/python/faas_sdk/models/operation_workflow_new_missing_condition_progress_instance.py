from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_new_missing_condition_progress_change import (
        OperationWorkflowNewMissingConditionProgressChange,
    )
    from ..models.operation_workflow_new_missing_condition_progress_count import (
        OperationWorkflowNewMissingConditionProgressCount,
    )
    from ..models.operation_workflow_state import OperationWorkflowState
    from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry


T = TypeVar("T", bound="OperationWorkflowNewMissingConditionProgressInstance")


@_attrs_define
class OperationWorkflowNewMissingConditionProgressInstance:
    """One current ongoing workflow with missing reports and a comparable retained baseline, ordered by stable workflow
    identity. Baseline_report identifies the pinned baseline definition source. A terminal baseline has no active
    expectations; newly active current gaps are new_expectation rather than reporting loss. Conditions use canonical
    current step and condition ID order. Customer projections omit tenant identifiers.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    baseline_report: OperationWorkflowStateHistoryEntry
    """One retained app-reported state update. Pages are ordered by revision, then stable publication and report
    identifiers."""
    baseline_terminal: bool
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    state_changed: bool
    current_missing_condition_count: int
    statuses: list[OperationWorkflowNewMissingConditionProgressCount]
    changes_truncated: bool
    changes: list[OperationWorkflowNewMissingConditionProgressChange]
    platform_tenant_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        baseline_report = self.baseline_report.to_dict()

        baseline_terminal = self.baseline_terminal

        state = self.state.to_dict()

        state_changed = self.state_changed

        current_missing_condition_count = self.current_missing_condition_count

        statuses = []
        for statuses_item_data in self.statuses:
            statuses_item = statuses_item_data.to_dict()
            statuses.append(statuses_item)

        changes_truncated = self.changes_truncated

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "baseline_report": baseline_report,
                "baseline_terminal": baseline_terminal,
                "state": state,
                "state_changed": state_changed,
                "current_missing_condition_count": current_missing_condition_count,
                "statuses": statuses,
                "changes_truncated": changes_truncated,
                "changes": changes,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_new_missing_condition_progress_change import (
            OperationWorkflowNewMissingConditionProgressChange,
        )
        from ..models.operation_workflow_new_missing_condition_progress_count import (
            OperationWorkflowNewMissingConditionProgressCount,
        )
        from ..models.operation_workflow_state import OperationWorkflowState
        from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        baseline_report = OperationWorkflowStateHistoryEntry.from_dict(d.pop("baseline_report"))

        baseline_terminal = d.pop("baseline_terminal")

        state = OperationWorkflowState.from_dict(d.pop("state"))

        state_changed = d.pop("state_changed")

        current_missing_condition_count = d.pop("current_missing_condition_count")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = OperationWorkflowNewMissingConditionProgressCount.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        changes_truncated = d.pop("changes_truncated")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = OperationWorkflowNewMissingConditionProgressChange.from_dict(changes_item_data)

            changes.append(changes_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_new_missing_condition_progress_instance = cls(
            subject=subject,
            baseline_report=baseline_report,
            baseline_terminal=baseline_terminal,
            state=state,
            state_changed=state_changed,
            current_missing_condition_count=current_missing_condition_count,
            statuses=statuses,
            changes_truncated=changes_truncated,
            changes=changes,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_new_missing_condition_progress_instance
