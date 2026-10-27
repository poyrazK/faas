from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_rule_comparison_single_version_status import (
    OperationWorkflowRuleComparisonSingleVersionStatus,
    check_operation_workflow_rule_comparison_single_version_status,
)
from ..models.operation_workflow_rule_comparison_status import (
    OperationWorkflowRuleComparisonStatus,
    check_operation_workflow_rule_comparison_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_rule_version_overlap import OperationWorkflowRuleVersionOverlap
    from ..models.operation_workflow_rule_version_statistics import OperationWorkflowRuleVersionStatistics


T = TypeVar("T", bound="OperationWorkflowRuleComparison")


@_attrs_define
class OperationWorkflowRuleComparison:
    """One pinned contract, decision code and rule ID with side-by-side version columns. Compared status means at least two
    returned versions have eligible observations; it is not a significance test or a claim of improvement. At most 16
    canonical identities and 16 lexical versions per identity are retained.

    """

    single_version_status: OperationWorkflowRuleComparisonSingleVersionStatus
    """At least two returned versions need eligible single-version workflows to compare this subset."""
    multi_version_workflow_count: int
    """Distinct sampled workflows reporting multiple versions for this comparison identity, including incomplete
    histories and versions omitted by limits."""
    eligible_multi_version_workflow_count: int
    examples_truncated: bool
    multi_version_examples: list[OperationWorkflowRuleVersionOverlap]
    contract_version: int
    decision_code: str
    rule_id: str
    status: OperationWorkflowRuleComparisonStatus
    versions_truncated: bool
    versions: list[OperationWorkflowRuleVersionStatistics]

    def to_dict(self) -> dict[str, Any]:
        single_version_status: str = self.single_version_status

        multi_version_workflow_count = self.multi_version_workflow_count

        eligible_multi_version_workflow_count = self.eligible_multi_version_workflow_count

        examples_truncated = self.examples_truncated

        multi_version_examples = []
        for multi_version_examples_item_data in self.multi_version_examples:
            multi_version_examples_item = multi_version_examples_item_data.to_dict()
            multi_version_examples.append(multi_version_examples_item)

        contract_version = self.contract_version

        decision_code = self.decision_code

        rule_id = self.rule_id

        status: str = self.status

        versions_truncated = self.versions_truncated

        versions = []
        for versions_item_data in self.versions:
            versions_item = versions_item_data.to_dict()
            versions.append(versions_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "single_version_status": single_version_status,
                "multi_version_workflow_count": multi_version_workflow_count,
                "eligible_multi_version_workflow_count": eligible_multi_version_workflow_count,
                "examples_truncated": examples_truncated,
                "multi_version_examples": multi_version_examples,
                "contract_version": contract_version,
                "decision_code": decision_code,
                "rule_id": rule_id,
                "status": status,
                "versions_truncated": versions_truncated,
                "versions": versions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_rule_version_overlap import OperationWorkflowRuleVersionOverlap
        from ..models.operation_workflow_rule_version_statistics import OperationWorkflowRuleVersionStatistics

        d = dict(src_dict)
        single_version_status = check_operation_workflow_rule_comparison_single_version_status(
            d.pop("single_version_status")
        )

        multi_version_workflow_count = d.pop("multi_version_workflow_count")

        eligible_multi_version_workflow_count = d.pop("eligible_multi_version_workflow_count")

        examples_truncated = d.pop("examples_truncated")

        multi_version_examples = []
        _multi_version_examples = d.pop("multi_version_examples")
        for multi_version_examples_item_data in _multi_version_examples:
            multi_version_examples_item = OperationWorkflowRuleVersionOverlap.from_dict(
                multi_version_examples_item_data
            )

            multi_version_examples.append(multi_version_examples_item)

        contract_version = d.pop("contract_version")

        decision_code = d.pop("decision_code")

        rule_id = d.pop("rule_id")

        status = check_operation_workflow_rule_comparison_status(d.pop("status"))

        versions_truncated = d.pop("versions_truncated")

        versions = []
        _versions = d.pop("versions")
        for versions_item_data in _versions:
            versions_item = OperationWorkflowRuleVersionStatistics.from_dict(versions_item_data)

            versions.append(versions_item)

        operation_workflow_rule_comparison = cls(
            single_version_status=single_version_status,
            multi_version_workflow_count=multi_version_workflow_count,
            eligible_multi_version_workflow_count=eligible_multi_version_workflow_count,
            examples_truncated=examples_truncated,
            multi_version_examples=multi_version_examples,
            contract_version=contract_version,
            decision_code=decision_code,
            rule_id=rule_id,
            status=status,
            versions_truncated=versions_truncated,
            versions=versions,
        )

        return operation_workflow_rule_comparison
