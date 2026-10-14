from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_impact_summary_kind import (
    OperationWorkflowReadinessImpactSummaryKind,
    check_operation_workflow_readiness_impact_summary_kind,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_impact_owner import OperationWorkflowReadinessImpactOwner


T = TypeVar("T", bound="OperationWorkflowReadinessImpactSummary")


@_attrs_define
class OperationWorkflowReadinessImpactSummary:
    """OperationWorkflowReadinessImpactSummary. Retained unresolved review confidence by workflow and protected business
    result, separate from reported business outcome.

    """

    workflow: str
    """OperationWorkflowReadinessImpactSummary.workflow. Scoped workflow identity; identical result codes in
    different workflows remain separate."""
    kind: OperationWorkflowReadinessImpactSummaryKind
    """OperationWorkflowReadinessImpactSummary.kind. Pinned protected result category."""
    code: str
    """OperationWorkflowReadinessImpactSummary.code. Pinned protected milestone or outcome code."""
    review_count: int
    """OperationWorkflowReadinessImpactSummary.review_count. Distinct progress/outcome/step/regression obligations
    across inbox deliveries."""
    task_count: int
    """OperationWorkflowReadinessImpactSummary.task_count. Matching retained inbox delivery tasks."""
    case_count: int
    """OperationWorkflowReadinessImpactSummary.case_count. Distinct tenant/subject/workflow/instance cases."""
    critical_count: int
    """OperationWorkflowReadinessImpactSummary.critical_count. Distinct critical review obligations."""
    high_count: int
    """OperationWorkflowReadinessImpactSummary.high_count. Distinct high-priority review obligations."""
    overdue_count: int
    """OperationWorkflowReadinessImpactSummary.overdue_count. Distinct review obligations with at least one overdue
    queue delivery."""
    critical_overdue_count: int
    """OperationWorkflowReadinessImpactSummary.critical_overdue_count. Distinct critical review obligations with
    overdue queue attention."""
    owners: list[OperationWorkflowReadinessImpactOwner]
    """OperationWorkflowReadinessImpactSummary.owners. Effective inbox ownership totals, ordered by overdue task
    count, task count and owner label. A review delivered to several teams contributes to each delivery's owner."""

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        kind: str = self.kind

        code = self.code

        review_count = self.review_count

        task_count = self.task_count

        case_count = self.case_count

        critical_count = self.critical_count

        high_count = self.high_count

        overdue_count = self.overdue_count

        critical_overdue_count = self.critical_overdue_count

        owners = []
        for owners_item_data in self.owners:
            owners_item = owners_item_data.to_dict()
            owners.append(owners_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "kind": kind,
                "code": code,
                "review_count": review_count,
                "task_count": task_count,
                "case_count": case_count,
                "critical_count": critical_count,
                "high_count": high_count,
                "overdue_count": overdue_count,
                "critical_overdue_count": critical_overdue_count,
                "owners": owners,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_impact_owner import OperationWorkflowReadinessImpactOwner

        d = dict(src_dict)
        workflow = d.pop("workflow")

        kind = check_operation_workflow_readiness_impact_summary_kind(d.pop("kind"))

        code = d.pop("code")

        review_count = d.pop("review_count")

        task_count = d.pop("task_count")

        case_count = d.pop("case_count")

        critical_count = d.pop("critical_count")

        high_count = d.pop("high_count")

        overdue_count = d.pop("overdue_count")

        critical_overdue_count = d.pop("critical_overdue_count")

        owners = []
        _owners = d.pop("owners")
        for owners_item_data in _owners:
            owners_item = OperationWorkflowReadinessImpactOwner.from_dict(owners_item_data)

            owners.append(owners_item)

        operation_workflow_readiness_impact_summary = cls(
            workflow=workflow,
            kind=kind,
            code=code,
            review_count=review_count,
            task_count=task_count,
            case_count=case_count,
            critical_count=critical_count,
            high_count=high_count,
            overdue_count=overdue_count,
            critical_overdue_count=critical_overdue_count,
            owners=owners,
        )

        return operation_workflow_readiness_impact_summary
