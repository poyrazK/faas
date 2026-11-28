from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_missing_condition_progress_change import (
        OperationWorkflowMissingConditionProgressChange,
    )
    from ..models.operation_workflow_missing_condition_progress_count import (
        OperationWorkflowMissingConditionProgressCount,
    )
    from ..models.operation_workflow_state import OperationWorkflowState
    from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry


T = TypeVar("T", bound="OperationWorkflowMissingConditionProgressInstance")


@_attrs_define
class OperationWorkflowMissingConditionProgressInstance:
    """One workflow with baseline missing expectations, ordered by stable workflow identity. Baseline_report anchors the
    reporting Operation and publication time whose pinned definition declares the before expectations. State is current;
    state_changed is observational. Customer projections omit tenant identifiers. Changes use canonical baseline step
    and condition ID order.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    baseline_report: OperationWorkflowStateHistoryEntry
    """One retained app-reported state update. Pages are ordered by revision, then stable publication and report
    identifiers."""
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    state_changed: bool
    baseline_missing_condition_count: int
    statuses: list[OperationWorkflowMissingConditionProgressCount]
    changes_truncated: bool
    changes: list[OperationWorkflowMissingConditionProgressChange]
    platform_tenant_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        baseline_report = self.baseline_report.to_dict()

        state = self.state.to_dict()

        state_changed = self.state_changed

        baseline_missing_condition_count = self.baseline_missing_condition_count

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
                "state": state,
                "state_changed": state_changed,
                "baseline_missing_condition_count": baseline_missing_condition_count,
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
        from ..models.operation_workflow_missing_condition_progress_change import (
            OperationWorkflowMissingConditionProgressChange,
        )
        from ..models.operation_workflow_missing_condition_progress_count import (
            OperationWorkflowMissingConditionProgressCount,
        )
        from ..models.operation_workflow_state import OperationWorkflowState
        from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        baseline_report = OperationWorkflowStateHistoryEntry.from_dict(d.pop("baseline_report"))

        state = OperationWorkflowState.from_dict(d.pop("state"))

        state_changed = d.pop("state_changed")

        baseline_missing_condition_count = d.pop("baseline_missing_condition_count")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = OperationWorkflowMissingConditionProgressCount.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        changes_truncated = d.pop("changes_truncated")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = OperationWorkflowMissingConditionProgressChange.from_dict(changes_item_data)

            changes.append(changes_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_missing_condition_progress_instance = cls(
            subject=subject,
            baseline_report=baseline_report,
            state=state,
            state_changed=state_changed,
            baseline_missing_condition_count=baseline_missing_condition_count,
            statuses=statuses,
            changes_truncated=changes_truncated,
            changes=changes,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_missing_condition_progress_instance
