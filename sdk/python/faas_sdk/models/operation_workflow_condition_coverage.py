from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_coverage_instance import OperationWorkflowConditionCoverageInstance
    from ..models.operation_workflow_missing_condition_group import OperationWorkflowMissingConditionGroup


T = TypeVar("T", bound="OperationWorkflowConditionCoverage")


@_attrs_define
class OperationWorkflowConditionCoverage:
    """Reporting coverage for current states in the bounded ongoing sample. Expectations come only from the latest
    reporting Operation's immutable definition, not a global registry or every Operation in the workflow. Missing means
    no matching evidence in latest explicit contexts, not a failed check or proof it was never reported historically.
    Full continuous retained history is required. Conflicting results and over-limit evidence are excluded. Reported
    includes overdue, so expected equals reported plus missing and overdue is a subset of reported. Counts precede the
    per-workflow preview cap. No expectations is unconfigured coverage, not completeness.

    """

    missing_groups: list[OperationWorkflowMissingConditionGroup]
    """Missing reports grouped by current contract and state and pinned refresh owner. Workflow counts are unique
    per group; one workflow may appear for several owners. Canonical contract/state/owner ordering includes
    unassigned. Counts precede condition previews; group display caps do not change total missing conditions."""
    missing_groups_truncated: bool
    matching_workflow_count: int
    sampled_workflow_count: int
    compared_workflow_count: int
    no_expectations_workflow_count: int
    excluded_incomplete_workflow_count: int
    excluded_ambiguous_workflow_count: int
    excluded_condition_evidence_workflow_count: int
    expected_condition_count: int
    reported_condition_count: int
    missing_condition_count: int
    overdue_condition_count: int
    cohort_truncated: bool
    items: list[OperationWorkflowConditionCoverageInstance]

    def to_dict(self) -> dict[str, Any]:
        missing_groups = []
        for missing_groups_item_data in self.missing_groups:
            missing_groups_item = missing_groups_item_data.to_dict()
            missing_groups.append(missing_groups_item)

        missing_groups_truncated = self.missing_groups_truncated

        matching_workflow_count = self.matching_workflow_count

        sampled_workflow_count = self.sampled_workflow_count

        compared_workflow_count = self.compared_workflow_count

        no_expectations_workflow_count = self.no_expectations_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        excluded_ambiguous_workflow_count = self.excluded_ambiguous_workflow_count

        excluded_condition_evidence_workflow_count = self.excluded_condition_evidence_workflow_count

        expected_condition_count = self.expected_condition_count

        reported_condition_count = self.reported_condition_count

        missing_condition_count = self.missing_condition_count

        overdue_condition_count = self.overdue_condition_count

        cohort_truncated = self.cohort_truncated

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "missing_groups": missing_groups,
                "missing_groups_truncated": missing_groups_truncated,
                "matching_workflow_count": matching_workflow_count,
                "sampled_workflow_count": sampled_workflow_count,
                "compared_workflow_count": compared_workflow_count,
                "no_expectations_workflow_count": no_expectations_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "excluded_ambiguous_workflow_count": excluded_ambiguous_workflow_count,
                "excluded_condition_evidence_workflow_count": excluded_condition_evidence_workflow_count,
                "expected_condition_count": expected_condition_count,
                "reported_condition_count": reported_condition_count,
                "missing_condition_count": missing_condition_count,
                "overdue_condition_count": overdue_condition_count,
                "cohort_truncated": cohort_truncated,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_coverage_instance import OperationWorkflowConditionCoverageInstance
        from ..models.operation_workflow_missing_condition_group import OperationWorkflowMissingConditionGroup

        d = dict(src_dict)
        missing_groups = []
        _missing_groups = d.pop("missing_groups")
        for missing_groups_item_data in _missing_groups:
            missing_groups_item = OperationWorkflowMissingConditionGroup.from_dict(missing_groups_item_data)

            missing_groups.append(missing_groups_item)

        missing_groups_truncated = d.pop("missing_groups_truncated")

        matching_workflow_count = d.pop("matching_workflow_count")

        sampled_workflow_count = d.pop("sampled_workflow_count")

        compared_workflow_count = d.pop("compared_workflow_count")

        no_expectations_workflow_count = d.pop("no_expectations_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        excluded_ambiguous_workflow_count = d.pop("excluded_ambiguous_workflow_count")

        excluded_condition_evidence_workflow_count = d.pop("excluded_condition_evidence_workflow_count")

        expected_condition_count = d.pop("expected_condition_count")

        reported_condition_count = d.pop("reported_condition_count")

        missing_condition_count = d.pop("missing_condition_count")

        overdue_condition_count = d.pop("overdue_condition_count")

        cohort_truncated = d.pop("cohort_truncated")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowConditionCoverageInstance.from_dict(items_item_data)

            items.append(items_item)

        operation_workflow_condition_coverage = cls(
            missing_groups=missing_groups,
            missing_groups_truncated=missing_groups_truncated,
            matching_workflow_count=matching_workflow_count,
            sampled_workflow_count=sampled_workflow_count,
            compared_workflow_count=compared_workflow_count,
            no_expectations_workflow_count=no_expectations_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            excluded_ambiguous_workflow_count=excluded_ambiguous_workflow_count,
            excluded_condition_evidence_workflow_count=excluded_condition_evidence_workflow_count,
            expected_condition_count=expected_condition_count,
            reported_condition_count=reported_condition_count,
            missing_condition_count=missing_condition_count,
            overdue_condition_count=overdue_condition_count,
            cohort_truncated=cohort_truncated,
            items=items,
        )

        return operation_workflow_condition_coverage
