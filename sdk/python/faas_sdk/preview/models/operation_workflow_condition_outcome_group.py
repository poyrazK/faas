from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_condition_outcome_group_condition_result import (
    OperationWorkflowConditionOutcomeGroupConditionResult,
    check_operation_workflow_condition_outcome_group_condition_result,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowConditionOutcomeGroup")


@_attrs_define
class OperationWorkflowConditionOutcomeGroup:
    """A workflow counts once per exact contract and decision and rule/version and condition ID/description and observed
    result and outcome. Different descriptions remain separate. Multiple observed results cause overlapping memberships.
    Durations cover the full completed workflow rather than time under this condition; no causal claim.

    """

    contract_version: int
    decision_code: str
    rule_id: str
    rule_version: str
    condition_id: str
    condition_description: str
    """Public condition vocabulary included in the exact grouping identity; UTF-8 byte bound without controls."""
    condition_result: OperationWorkflowConditionOutcomeGroupConditionResult
    outcome_code: str
    changing_result_workflow_count: int
    """Contributors reporting more than one result for this exact condition identity before or at the first
    terminal report."""
    completion_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    blocked_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""

    def to_dict(self) -> dict[str, Any]:
        contract_version = self.contract_version

        decision_code = self.decision_code

        rule_id = self.rule_id

        rule_version = self.rule_version

        condition_id = self.condition_id

        condition_description = self.condition_description

        condition_result: str = self.condition_result

        outcome_code = self.outcome_code

        changing_result_workflow_count = self.changing_result_workflow_count

        completion_time = self.completion_time.to_dict()

        blocked_time = self.blocked_time.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "contract_version": contract_version,
                "decision_code": decision_code,
                "rule_id": rule_id,
                "rule_version": rule_version,
                "condition_id": condition_id,
                "condition_description": condition_description,
                "condition_result": condition_result,
                "outcome_code": outcome_code,
                "changing_result_workflow_count": changing_result_workflow_count,
                "completion_time": completion_time,
                "blocked_time": blocked_time,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution

        d = dict(src_dict)
        contract_version = d.pop("contract_version")

        decision_code = d.pop("decision_code")

        rule_id = d.pop("rule_id")

        rule_version = d.pop("rule_version")

        condition_id = d.pop("condition_id")

        condition_description = d.pop("condition_description")

        condition_result = check_operation_workflow_condition_outcome_group_condition_result(d.pop("condition_result"))

        outcome_code = d.pop("outcome_code")

        changing_result_workflow_count = d.pop("changing_result_workflow_count")

        completion_time = OperationWorkflowDurationDistribution.from_dict(d.pop("completion_time"))

        blocked_time = OperationWorkflowDurationDistribution.from_dict(d.pop("blocked_time"))

        operation_workflow_condition_outcome_group = cls(
            contract_version=contract_version,
            decision_code=decision_code,
            rule_id=rule_id,
            rule_version=rule_version,
            condition_id=condition_id,
            condition_description=condition_description,
            condition_result=condition_result,
            outcome_code=outcome_code,
            changing_result_workflow_count=changing_result_workflow_count,
            completion_time=completion_time,
            blocked_time=blocked_time,
        )

        return operation_workflow_condition_outcome_group
