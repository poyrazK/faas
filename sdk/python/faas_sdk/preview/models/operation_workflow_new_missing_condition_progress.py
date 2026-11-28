from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_gap_progress_group import OperationWorkflowGapProgressGroup
    from ..models.operation_workflow_new_missing_condition_progress_count import (
        OperationWorkflowNewMissingConditionProgressCount,
    )
    from ..models.operation_workflow_new_missing_condition_progress_instance import (
        OperationWorkflowNewMissingConditionProgressInstance,
    )


T = TypeVar("T", bound="OperationWorkflowNewMissingConditionProgress")


@_attrs_define
class OperationWorkflowNewMissingConditionProgress:
    """Optional baseline_token comparison explaining current missing reports relative to retained baseline expectations in
    today's completed and ongoing sample. Current terminal workflows produce no active gaps. Publication visibility,
    retained immutable baseline definitions, continuous baseline report history, conflict and evidence limits match
    missing_progress. Workflows with no retained baseline are not invented as new gaps, and unavailable definitions are
    excluded. This is not original cohort membership or total historical backlog. All current missing status counts
    precede preview caps; missing evidence is not failed verification.

    """

    groups: list[OperationWorkflowGapProgressGroup]
    groups_truncated: bool
    baseline_at: datetime.datetime
    evaluated_at: datetime.datetime
    sampled_workflow_count: int
    compared_workflow_count: int
    no_baseline_workflow_count: int
    no_current_missing_workflow_count: int
    excluded_incomplete_workflow_count: int
    excluded_ambiguous_workflow_count: int
    excluded_condition_evidence_workflow_count: int
    excluded_definition_workflow_count: int
    statuses: list[OperationWorkflowNewMissingConditionProgressCount]
    items: list[OperationWorkflowNewMissingConditionProgressInstance]

    def to_dict(self) -> dict[str, Any]:
        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        groups_truncated = self.groups_truncated

        baseline_at = self.baseline_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        sampled_workflow_count = self.sampled_workflow_count

        compared_workflow_count = self.compared_workflow_count

        no_baseline_workflow_count = self.no_baseline_workflow_count

        no_current_missing_workflow_count = self.no_current_missing_workflow_count

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
                "groups": groups,
                "groups_truncated": groups_truncated,
                "baseline_at": baseline_at,
                "evaluated_at": evaluated_at,
                "sampled_workflow_count": sampled_workflow_count,
                "compared_workflow_count": compared_workflow_count,
                "no_baseline_workflow_count": no_baseline_workflow_count,
                "no_current_missing_workflow_count": no_current_missing_workflow_count,
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
        from ..models.operation_workflow_gap_progress_group import OperationWorkflowGapProgressGroup
        from ..models.operation_workflow_new_missing_condition_progress_count import (
            OperationWorkflowNewMissingConditionProgressCount,
        )
        from ..models.operation_workflow_new_missing_condition_progress_instance import (
            OperationWorkflowNewMissingConditionProgressInstance,
        )

        d = dict(src_dict)
        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = OperationWorkflowGapProgressGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        groups_truncated = d.pop("groups_truncated")

        baseline_at = datetime.datetime.fromisoformat(d.pop("baseline_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        sampled_workflow_count = d.pop("sampled_workflow_count")

        compared_workflow_count = d.pop("compared_workflow_count")

        no_baseline_workflow_count = d.pop("no_baseline_workflow_count")

        no_current_missing_workflow_count = d.pop("no_current_missing_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        excluded_ambiguous_workflow_count = d.pop("excluded_ambiguous_workflow_count")

        excluded_condition_evidence_workflow_count = d.pop("excluded_condition_evidence_workflow_count")

        excluded_definition_workflow_count = d.pop("excluded_definition_workflow_count")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = OperationWorkflowNewMissingConditionProgressCount.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowNewMissingConditionProgressInstance.from_dict(items_item_data)

            items.append(items_item)

        operation_workflow_new_missing_condition_progress = cls(
            groups=groups,
            groups_truncated=groups_truncated,
            baseline_at=baseline_at,
            evaluated_at=evaluated_at,
            sampled_workflow_count=sampled_workflow_count,
            compared_workflow_count=compared_workflow_count,
            no_baseline_workflow_count=no_baseline_workflow_count,
            no_current_missing_workflow_count=no_current_missing_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            excluded_ambiguous_workflow_count=excluded_ambiguous_workflow_count,
            excluded_condition_evidence_workflow_count=excluded_condition_evidence_workflow_count,
            excluded_definition_workflow_count=excluded_definition_workflow_count,
            statuses=statuses,
            items=items,
        )

        return operation_workflow_new_missing_condition_progress
