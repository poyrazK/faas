from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_rule_comparison import OperationWorkflowRuleComparison


T = TypeVar("T", bound="OperationWorkflowRuleComparisonSummary")


@_attrs_define
class OperationWorkflowRuleComparisonSummary:
    """Side-by-side rule version statistics from the same bounded completed workflow sample. Observed identities are scoped
    to contract, decision code and rule ID. Version names are opaque and sorted lexically, not chronologically.
    Workflows may report several versions and contribute to several columns; comparisons do not establish causation or
    statistical significance. Coverage of unobserved versions cannot be inferred from missing history.

    """

    sampled_workflow_count: int
    unattributed_workflow_count: int
    """Sampled workflows without an observed decision identity in the retained window. Includes missing history and
    paths without decisions; absence does not prove a rule was unused."""
    comparisons_truncated: bool
    comparisons: list[OperationWorkflowRuleComparison]

    def to_dict(self) -> dict[str, Any]:
        sampled_workflow_count = self.sampled_workflow_count

        unattributed_workflow_count = self.unattributed_workflow_count

        comparisons_truncated = self.comparisons_truncated

        comparisons = []
        for comparisons_item_data in self.comparisons:
            comparisons_item = comparisons_item_data.to_dict()
            comparisons.append(comparisons_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "sampled_workflow_count": sampled_workflow_count,
                "unattributed_workflow_count": unattributed_workflow_count,
                "comparisons_truncated": comparisons_truncated,
                "comparisons": comparisons,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_rule_comparison import OperationWorkflowRuleComparison

        d = dict(src_dict)
        sampled_workflow_count = d.pop("sampled_workflow_count")

        unattributed_workflow_count = d.pop("unattributed_workflow_count")

        comparisons_truncated = d.pop("comparisons_truncated")

        comparisons = []
        _comparisons = d.pop("comparisons")
        for comparisons_item_data in _comparisons:
            comparisons_item = OperationWorkflowRuleComparison.from_dict(comparisons_item_data)

            comparisons.append(comparisons_item)

        operation_workflow_rule_comparison_summary = cls(
            sampled_workflow_count=sampled_workflow_count,
            unattributed_workflow_count=unattributed_workflow_count,
            comparisons_truncated=comparisons_truncated,
            comparisons=comparisons,
        )

        return operation_workflow_rule_comparison_summary
