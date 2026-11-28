from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_active_condition_group_condition_result import (
    OperationWorkflowActiveConditionGroupConditionResult,
    check_operation_workflow_active_condition_group_condition_result,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowActiveConditionGroup")


@_attrs_define
class OperationWorkflowActiveConditionGroup:
    """One latest explicitly reported result per exact condition identity and workflow. Elapsed and blocked durations cover
    the entire observed workflow to evaluated_at, not time under the condition. Evidence age is measured at the same
    time. Workflow state staleness follows its declared threshold and does not expire the condition.

    """

    within_budget_workflow_count: int
    overdue_workflow_count: int
    no_budget_workflow_count: int
    contract_version: int
    decision_code: str
    rule_id: str
    rule_version: str
    condition_id: str
    condition_description: str
    condition_result: OperationWorkflowActiveConditionGroupConditionResult
    stale_workflow_count: int
    elapsed_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    blocked_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    evidence_age: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""

    def to_dict(self) -> dict[str, Any]:
        within_budget_workflow_count = self.within_budget_workflow_count

        overdue_workflow_count = self.overdue_workflow_count

        no_budget_workflow_count = self.no_budget_workflow_count

        contract_version = self.contract_version

        decision_code = self.decision_code

        rule_id = self.rule_id

        rule_version = self.rule_version

        condition_id = self.condition_id

        condition_description = self.condition_description

        condition_result: str = self.condition_result

        stale_workflow_count = self.stale_workflow_count

        elapsed_time = self.elapsed_time.to_dict()

        blocked_time = self.blocked_time.to_dict()

        evidence_age = self.evidence_age.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "within_budget_workflow_count": within_budget_workflow_count,
                "overdue_workflow_count": overdue_workflow_count,
                "no_budget_workflow_count": no_budget_workflow_count,
                "contract_version": contract_version,
                "decision_code": decision_code,
                "rule_id": rule_id,
                "rule_version": rule_version,
                "condition_id": condition_id,
                "condition_description": condition_description,
                "condition_result": condition_result,
                "stale_workflow_count": stale_workflow_count,
                "elapsed_time": elapsed_time,
                "blocked_time": blocked_time,
                "evidence_age": evidence_age,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution

        d = dict(src_dict)
        within_budget_workflow_count = d.pop("within_budget_workflow_count")

        overdue_workflow_count = d.pop("overdue_workflow_count")

        no_budget_workflow_count = d.pop("no_budget_workflow_count")

        contract_version = d.pop("contract_version")

        decision_code = d.pop("decision_code")

        rule_id = d.pop("rule_id")

        rule_version = d.pop("rule_version")

        condition_id = d.pop("condition_id")

        condition_description = d.pop("condition_description")

        condition_result = check_operation_workflow_active_condition_group_condition_result(d.pop("condition_result"))

        stale_workflow_count = d.pop("stale_workflow_count")

        elapsed_time = OperationWorkflowDurationDistribution.from_dict(d.pop("elapsed_time"))

        blocked_time = OperationWorkflowDurationDistribution.from_dict(d.pop("blocked_time"))

        evidence_age = OperationWorkflowDurationDistribution.from_dict(d.pop("evidence_age"))

        operation_workflow_active_condition_group = cls(
            within_budget_workflow_count=within_budget_workflow_count,
            overdue_workflow_count=overdue_workflow_count,
            no_budget_workflow_count=no_budget_workflow_count,
            contract_version=contract_version,
            decision_code=decision_code,
            rule_id=rule_id,
            rule_version=rule_version,
            condition_id=condition_id,
            condition_description=condition_description,
            condition_result=condition_result,
            stale_workflow_count=stale_workflow_count,
            elapsed_time=elapsed_time,
            blocked_time=blocked_time,
            evidence_age=evidence_age,
        )

        return operation_workflow_active_condition_group
