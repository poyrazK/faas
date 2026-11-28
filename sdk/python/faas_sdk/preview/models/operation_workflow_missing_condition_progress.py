from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_missing_condition_progress_count import (
        OperationWorkflowMissingConditionProgressCount,
    )
    from ..models.operation_workflow_missing_condition_progress_instance import (
        OperationWorkflowMissingConditionProgressInstance,
    )


T = TypeVar("T", bound="OperationWorkflowMissingConditionProgress")


@_attrs_define
class OperationWorkflowMissingConditionProgress:
    """Optional baseline_token comparison of missing expectations reconstructed from retained reports and the baseline
    reporting Operation's immutable definition in today's completed and ongoing samples. Publication timestamps
    determine baseline visibility. This does not store original cohort membership or prove backlog resolution. Missing
    historical definitions, incomplete report history, conflicts and evidence limits are explicitly excluded. Baseline
    histories must be continuous within one contract; current contract boundaries permit contract_changed
    classification. Prior terminal reports do not create baseline gaps. Counts precede change preview limits. Conditions
    leaving the current state's expectation set, changed declarations and terminal workflows do not imply successful
    verification.

    """

    baseline_at: datetime.datetime
    evaluated_at: datetime.datetime
    sampled_workflow_count: int
    compared_workflow_count: int
    no_baseline_workflow_count: int
    no_missing_baseline_workflow_count: int
    excluded_incomplete_workflow_count: int
    excluded_ambiguous_workflow_count: int
    excluded_condition_evidence_workflow_count: int
    excluded_definition_workflow_count: int
    statuses: list[OperationWorkflowMissingConditionProgressCount]
    items: list[OperationWorkflowMissingConditionProgressInstance]

    def to_dict(self) -> dict[str, Any]:
        baseline_at = self.baseline_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        sampled_workflow_count = self.sampled_workflow_count

        compared_workflow_count = self.compared_workflow_count

        no_baseline_workflow_count = self.no_baseline_workflow_count

        no_missing_baseline_workflow_count = self.no_missing_baseline_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        excluded_ambiguous_workflow_count = self.excluded_ambiguous_workflow_count

        excluded_condition_evidence_workflow_count = self.excluded_condition_evidence_workflow_count

        excluded_definition_workflow_count = self.excluded_definition_workflow_count

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
                "no_missing_baseline_workflow_count": no_missing_baseline_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "excluded_ambiguous_workflow_count": excluded_ambiguous_workflow_count,
                "excluded_condition_evidence_workflow_count": excluded_condition_evidence_workflow_count,
                "excluded_definition_workflow_count": excluded_definition_workflow_count,
                "statuses": statuses,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_missing_condition_progress_count import (
            OperationWorkflowMissingConditionProgressCount,
        )
        from ..models.operation_workflow_missing_condition_progress_instance import (
            OperationWorkflowMissingConditionProgressInstance,
        )

        d = dict(src_dict)
        baseline_at = datetime.datetime.fromisoformat(d.pop("baseline_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        sampled_workflow_count = d.pop("sampled_workflow_count")

        compared_workflow_count = d.pop("compared_workflow_count")

        no_baseline_workflow_count = d.pop("no_baseline_workflow_count")

        no_missing_baseline_workflow_count = d.pop("no_missing_baseline_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        excluded_ambiguous_workflow_count = d.pop("excluded_ambiguous_workflow_count")

        excluded_condition_evidence_workflow_count = d.pop("excluded_condition_evidence_workflow_count")

        excluded_definition_workflow_count = d.pop("excluded_definition_workflow_count")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = OperationWorkflowMissingConditionProgressCount.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowMissingConditionProgressInstance.from_dict(items_item_data)

            items.append(items_item)

        operation_workflow_missing_condition_progress = cls(
            baseline_at=baseline_at,
            evaluated_at=evaluated_at,
            sampled_workflow_count=sampled_workflow_count,
            compared_workflow_count=compared_workflow_count,
            no_baseline_workflow_count=no_baseline_workflow_count,
            no_missing_baseline_workflow_count=no_missing_baseline_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            excluded_ambiguous_workflow_count=excluded_ambiguous_workflow_count,
            excluded_condition_evidence_workflow_count=excluded_condition_evidence_workflow_count,
            excluded_definition_workflow_count=excluded_definition_workflow_count,
            statuses=statuses,
            items=items,
        )

        return operation_workflow_missing_condition_progress
