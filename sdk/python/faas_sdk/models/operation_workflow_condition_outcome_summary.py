from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_outcome_group import OperationWorkflowConditionOutcomeGroup


T = TypeVar("T", bound="OperationWorkflowConditionOutcomeSummary")


@_attrs_define
class OperationWorkflowConditionOutcomeSummary:
    """Observed condition/result associations with latest reported outcomes in the completed performance sample. Only
    complete histories with a reported outcome and at most 256 exact condition identities qualify. Reasons and selected-
    path decision facts at or before the first terminal report supply explicit results. Missing conditions are
    unreported; unknown is explicit. Groups overlap and do not establish causation.

    """

    matching_workflow_count: int
    """All completed matching workflows before bounded sampling."""
    sampled_workflow_count: int
    linked_workflow_count: int
    """Eligible sampled workflows with reported condition evidence; includes workflows whose groups are omitted by
    the display cap."""
    changing_result_workflow_count: int
    """Eligible linked workflows reporting multiple results for at least one exact condition and rule identity;
    counted once per workflow."""
    excluded_incomplete_workflow_count: int
    missing_outcome_workflow_count: int
    unclassified_workflow_count: int
    """Complete sampled workflows with an outcome but no reported conditions; omission is not unknown."""
    excluded_condition_evidence_workflow_count: int
    """Complete workflows with an outcome exceeding 256 exact condition identities; excluded entirely before group
    display limits."""
    cohort_truncated: bool
    groups_truncated: bool
    groups: list[OperationWorkflowConditionOutcomeGroup]
    """First 64 identities in contract/decision/rule/version/condition ID/description/result/outcome lexical order.
    Counts of overlapping groups must not be summed."""

    def to_dict(self) -> dict[str, Any]:
        matching_workflow_count = self.matching_workflow_count

        sampled_workflow_count = self.sampled_workflow_count

        linked_workflow_count = self.linked_workflow_count

        changing_result_workflow_count = self.changing_result_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        missing_outcome_workflow_count = self.missing_outcome_workflow_count

        unclassified_workflow_count = self.unclassified_workflow_count

        excluded_condition_evidence_workflow_count = self.excluded_condition_evidence_workflow_count

        cohort_truncated = self.cohort_truncated

        groups_truncated = self.groups_truncated

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "matching_workflow_count": matching_workflow_count,
                "sampled_workflow_count": sampled_workflow_count,
                "linked_workflow_count": linked_workflow_count,
                "changing_result_workflow_count": changing_result_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "missing_outcome_workflow_count": missing_outcome_workflow_count,
                "unclassified_workflow_count": unclassified_workflow_count,
                "excluded_condition_evidence_workflow_count": excluded_condition_evidence_workflow_count,
                "cohort_truncated": cohort_truncated,
                "groups_truncated": groups_truncated,
                "groups": groups,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_outcome_group import OperationWorkflowConditionOutcomeGroup

        d = dict(src_dict)
        matching_workflow_count = d.pop("matching_workflow_count")

        sampled_workflow_count = d.pop("sampled_workflow_count")

        linked_workflow_count = d.pop("linked_workflow_count")

        changing_result_workflow_count = d.pop("changing_result_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        missing_outcome_workflow_count = d.pop("missing_outcome_workflow_count")

        unclassified_workflow_count = d.pop("unclassified_workflow_count")

        excluded_condition_evidence_workflow_count = d.pop("excluded_condition_evidence_workflow_count")

        cohort_truncated = d.pop("cohort_truncated")

        groups_truncated = d.pop("groups_truncated")

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = OperationWorkflowConditionOutcomeGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        operation_workflow_condition_outcome_summary = cls(
            matching_workflow_count=matching_workflow_count,
            sampled_workflow_count=sampled_workflow_count,
            linked_workflow_count=linked_workflow_count,
            changing_result_workflow_count=changing_result_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            missing_outcome_workflow_count=missing_outcome_workflow_count,
            unclassified_workflow_count=unclassified_workflow_count,
            excluded_condition_evidence_workflow_count=excluded_condition_evidence_workflow_count,
            cohort_truncated=cohort_truncated,
            groups_truncated=groups_truncated,
            groups=groups,
        )

        return operation_workflow_condition_outcome_summary
