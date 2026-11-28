from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowConditionCoverageInstance")


@_attrs_define
class OperationWorkflowConditionCoverageInstance:
    """Coverage for one ongoing workflow. Conditions are ordered by declaring step then condition ID. Exact totals include
    preview omissions; workflows are ordered by stable identity.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    expected_condition_count: int
    reported_condition_count: int
    missing_condition_count: int
    overdue_condition_count: int
    conditions_truncated: bool
    conditions: list[OperationWorkflowConditionCoverageEntry]
    platform_tenant_id: UUID | Unset = UNSET
    """Operator only; omitted in customer projections."""

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        state = self.state.to_dict()

        expected_condition_count = self.expected_condition_count

        reported_condition_count = self.reported_condition_count

        missing_condition_count = self.missing_condition_count

        overdue_condition_count = self.overdue_condition_count

        conditions_truncated = self.conditions_truncated

        conditions = []
        for conditions_item_data in self.conditions:
            conditions_item = conditions_item_data.to_dict()
            conditions.append(conditions_item)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "state": state,
                "expected_condition_count": expected_condition_count,
                "reported_condition_count": reported_condition_count,
                "missing_condition_count": missing_condition_count,
                "overdue_condition_count": overdue_condition_count,
                "conditions_truncated": conditions_truncated,
                "conditions": conditions,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        state = OperationWorkflowState.from_dict(d.pop("state"))

        expected_condition_count = d.pop("expected_condition_count")

        reported_condition_count = d.pop("reported_condition_count")

        missing_condition_count = d.pop("missing_condition_count")

        overdue_condition_count = d.pop("overdue_condition_count")

        conditions_truncated = d.pop("conditions_truncated")

        conditions = []
        _conditions = d.pop("conditions")
        for conditions_item_data in _conditions:
            conditions_item = OperationWorkflowConditionCoverageEntry.from_dict(conditions_item_data)

            conditions.append(conditions_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_condition_coverage_instance = cls(
            subject=subject,
            state=state,
            expected_condition_count=expected_condition_count,
            reported_condition_count=reported_condition_count,
            missing_condition_count=missing_condition_count,
            overdue_condition_count=overdue_condition_count,
            conditions_truncated=conditions_truncated,
            conditions=conditions,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_condition_coverage_instance
