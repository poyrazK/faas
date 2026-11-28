from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_condition_refresh_progress_change import (
        OperationWorkflowConditionRefreshProgressChange,
    )
    from ..models.operation_workflow_condition_refresh_progress_count import (
        OperationWorkflowConditionRefreshProgressCount,
    )
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowConditionRefreshProgressInstance")


@_attrs_define
class OperationWorkflowConditionRefreshProgressInstance:
    """One currently sampled workflow with overdue reporting evidence at the reconstructed baseline. Subject, current state
    and optional operator tenant context support workflow links. Customer projections omit tenant identifiers. Current
    terminal state does not imply reporting freshness or business verification success.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    baseline_overdue_condition_count: int
    newer_report_condition_count: int
    statuses: list[OperationWorkflowConditionRefreshProgressCount]
    changes_truncated: bool
    changes: list[OperationWorkflowConditionRefreshProgressChange]
    """First 16 overdue baseline identities in canonical order with before and optional exact-identity after
    evidence; status counts remain exact."""
    platform_tenant_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        state = self.state.to_dict()

        baseline_overdue_condition_count = self.baseline_overdue_condition_count

        newer_report_condition_count = self.newer_report_condition_count

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
                "state": state,
                "baseline_overdue_condition_count": baseline_overdue_condition_count,
                "newer_report_condition_count": newer_report_condition_count,
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
        from ..models.operation_workflow_condition_refresh_progress_change import (
            OperationWorkflowConditionRefreshProgressChange,
        )
        from ..models.operation_workflow_condition_refresh_progress_count import (
            OperationWorkflowConditionRefreshProgressCount,
        )
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        state = OperationWorkflowState.from_dict(d.pop("state"))

        baseline_overdue_condition_count = d.pop("baseline_overdue_condition_count")

        newer_report_condition_count = d.pop("newer_report_condition_count")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = OperationWorkflowConditionRefreshProgressCount.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        changes_truncated = d.pop("changes_truncated")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = OperationWorkflowConditionRefreshProgressChange.from_dict(changes_item_data)

            changes.append(changes_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_condition_refresh_progress_instance = cls(
            subject=subject,
            state=state,
            baseline_overdue_condition_count=baseline_overdue_condition_count,
            newer_report_condition_count=newer_report_condition_count,
            statuses=statuses,
            changes_truncated=changes_truncated,
            changes=changes,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_condition_refresh_progress_instance
