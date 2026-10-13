from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_refresh_progress_count import (
        OperationWorkflowConditionRefreshProgressCount,
    )
    from ..models.operation_workflow_condition_refresh_progress_instance import (
        OperationWorkflowConditionRefreshProgressInstance,
    )


T = TypeVar("T", bound="OperationWorkflowConditionRefreshProgress")


@_attrs_define
class OperationWorkflowConditionRefreshProgress:
    """Report freshness progress reconstructed at baseline_token evaluation time using publication visibility in current
    retained completed and ongoing cohorts. This is not a stored baseline cohort, a total historical backlog, or
    business verification. Only continuous retained report history qualifies; contract boundaries are permitted for
    explicit contract_changed classification without relaxing existing duration or active-summary eligibility. Unknown
    history and conflicting or over-limit evidence are excluded. Status totals include all baseline overdue identities
    before per-workflow preview limits. Completed workflows remain visible as terminal reports without implying cleared
    verification.

    """

    baseline_at: datetime.datetime
    evaluated_at: datetime.datetime
    sampled_workflow_count: int
    compared_workflow_count: int
    no_baseline_workflow_count: int
    no_overdue_baseline_workflow_count: int
    excluded_incomplete_workflow_count: int
    excluded_ambiguous_workflow_count: int
    excluded_condition_evidence_workflow_count: int
    newer_report_condition_count: int
    statuses: list[OperationWorkflowConditionRefreshProgressCount]
    items: list[OperationWorkflowConditionRefreshProgressInstance]
    """Compared workflows in stable workflow identity order from current samples of up to 100 completed and 100
    ongoing runs. Missing original baseline members are not classified as cleared."""

    def to_dict(self) -> dict[str, Any]:
        baseline_at = self.baseline_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        sampled_workflow_count = self.sampled_workflow_count

        compared_workflow_count = self.compared_workflow_count

        no_baseline_workflow_count = self.no_baseline_workflow_count

        no_overdue_baseline_workflow_count = self.no_overdue_baseline_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        excluded_ambiguous_workflow_count = self.excluded_ambiguous_workflow_count

        excluded_condition_evidence_workflow_count = self.excluded_condition_evidence_workflow_count

        newer_report_condition_count = self.newer_report_condition_count

        statuses = []
        for statuses_item_data in self.statuses:
            statuses_item = statuses_item_data.to_dict()
            statuses.append(statuses_item)

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "baseline_at": baseline_at,
                "evaluated_at": evaluated_at,
                "sampled_workflow_count": sampled_workflow_count,
                "compared_workflow_count": compared_workflow_count,
                "no_baseline_workflow_count": no_baseline_workflow_count,
                "no_overdue_baseline_workflow_count": no_overdue_baseline_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "excluded_ambiguous_workflow_count": excluded_ambiguous_workflow_count,
                "excluded_condition_evidence_workflow_count": excluded_condition_evidence_workflow_count,
                "newer_report_condition_count": newer_report_condition_count,
                "statuses": statuses,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_refresh_progress_count import (
            OperationWorkflowConditionRefreshProgressCount,
        )
        from ..models.operation_workflow_condition_refresh_progress_instance import (
            OperationWorkflowConditionRefreshProgressInstance,
        )

        d = dict(src_dict)
        baseline_at = datetime.datetime.fromisoformat(d.pop("baseline_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        sampled_workflow_count = d.pop("sampled_workflow_count")

        compared_workflow_count = d.pop("compared_workflow_count")

        no_baseline_workflow_count = d.pop("no_baseline_workflow_count")

        no_overdue_baseline_workflow_count = d.pop("no_overdue_baseline_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        excluded_ambiguous_workflow_count = d.pop("excluded_ambiguous_workflow_count")

        excluded_condition_evidence_workflow_count = d.pop("excluded_condition_evidence_workflow_count")

        newer_report_condition_count = d.pop("newer_report_condition_count")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = OperationWorkflowConditionRefreshProgressCount.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowConditionRefreshProgressInstance.from_dict(items_item_data)

            items.append(items_item)

        operation_workflow_condition_refresh_progress = cls(
            baseline_at=baseline_at,
            evaluated_at=evaluated_at,
            sampled_workflow_count=sampled_workflow_count,
            compared_workflow_count=compared_workflow_count,
            no_baseline_workflow_count=no_baseline_workflow_count,
            no_overdue_baseline_workflow_count=no_overdue_baseline_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            excluded_ambiguous_workflow_count=excluded_ambiguous_workflow_count,
            excluded_condition_evidence_workflow_count=excluded_condition_evidence_workflow_count,
            newer_report_condition_count=newer_report_condition_count,
            statuses=statuses,
            items=items,
        )

        return operation_workflow_condition_refresh_progress
