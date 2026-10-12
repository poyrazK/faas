from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_decision_outcome_group import OperationWorkflowDecisionOutcomeGroup
    from ..models.operation_workflow_rule_comparison_summary import OperationWorkflowRuleComparisonSummary


T = TypeVar("T", bound="OperationWorkflowDecisionOutcomeSummary")


@_attrs_define
class OperationWorkflowDecisionOutcomeSummary:
    """Observed decision and path associations with latest reported outcomes in the completed performance cohort. Only
    complete histories with a reported outcome qualify. Repeated decision or path identities count once per workflow and
    group. Groups overlap; their counts must not be summed. Classification uses reasons and selections reported at or
    before the first terminal report. Sequence does not establish causation. Groups retain the first 64 canonical
    identities in ascending order.

    """

    matching_workflow_count: int
    """All matching completed workflows before bounded sampling."""
    sampled_workflow_count: int
    linked_workflow_count: int
    """Sampled complete workflows with a reported outcome and at least one decision or path identity. Includes
    workflows whose groups are omitted by the group limit."""
    excluded_incomplete_workflow_count: int
    missing_outcome_workflow_count: int
    """Complete completed workflows without a latest reported outcome code."""
    unclassified_workflow_count: int
    """Complete completed workflows with an outcome but no qualifying decision or path."""
    cohort_truncated: bool
    groups_truncated: bool
    groups: list[OperationWorkflowDecisionOutcomeGroup]
    rule_comparisons: OperationWorkflowRuleComparisonSummary | Unset = UNSET
    """Side-by-side rule version statistics from the same bounded completed workflow sample. Observed identities
    are scoped to contract, decision code and rule ID. Version names are opaque and sorted lexically, not
    chronologically. Workflows may report several versions and contribute to several columns; comparisons do not
    establish causation or statistical significance. Coverage of unobserved versions cannot be inferred from missing
    history."""

    def to_dict(self) -> dict[str, Any]:
        matching_workflow_count = self.matching_workflow_count

        sampled_workflow_count = self.sampled_workflow_count

        linked_workflow_count = self.linked_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        missing_outcome_workflow_count = self.missing_outcome_workflow_count

        unclassified_workflow_count = self.unclassified_workflow_count

        cohort_truncated = self.cohort_truncated

        groups_truncated = self.groups_truncated

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        rule_comparisons: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rule_comparisons, Unset):
            rule_comparisons = self.rule_comparisons.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "matching_workflow_count": matching_workflow_count,
                "sampled_workflow_count": sampled_workflow_count,
                "linked_workflow_count": linked_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "missing_outcome_workflow_count": missing_outcome_workflow_count,
                "unclassified_workflow_count": unclassified_workflow_count,
                "cohort_truncated": cohort_truncated,
                "groups_truncated": groups_truncated,
                "groups": groups,
            }
        )
        if rule_comparisons is not UNSET:
            field_dict["rule_comparisons"] = rule_comparisons

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_decision_outcome_group import OperationWorkflowDecisionOutcomeGroup
        from ..models.operation_workflow_rule_comparison_summary import OperationWorkflowRuleComparisonSummary

        d = dict(src_dict)
        matching_workflow_count = d.pop("matching_workflow_count")

        sampled_workflow_count = d.pop("sampled_workflow_count")

        linked_workflow_count = d.pop("linked_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        missing_outcome_workflow_count = d.pop("missing_outcome_workflow_count")

        unclassified_workflow_count = d.pop("unclassified_workflow_count")

        cohort_truncated = d.pop("cohort_truncated")

        groups_truncated = d.pop("groups_truncated")

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = OperationWorkflowDecisionOutcomeGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        _rule_comparisons = d.pop("rule_comparisons", UNSET)
        rule_comparisons: OperationWorkflowRuleComparisonSummary | Unset
        if isinstance(_rule_comparisons, Unset):
            rule_comparisons = UNSET
        else:
            rule_comparisons = OperationWorkflowRuleComparisonSummary.from_dict(_rule_comparisons)

        operation_workflow_decision_outcome_summary = cls(
            matching_workflow_count=matching_workflow_count,
            sampled_workflow_count=sampled_workflow_count,
            linked_workflow_count=linked_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            missing_outcome_workflow_count=missing_outcome_workflow_count,
            unclassified_workflow_count=unclassified_workflow_count,
            cohort_truncated=cohort_truncated,
            groups_truncated=groups_truncated,
            groups=groups,
            rule_comparisons=rule_comparisons,
        )

        return operation_workflow_decision_outcome_summary
