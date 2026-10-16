from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_decision_outcome_group_kind import (
    OperationWorkflowDecisionOutcomeGroupKind,
    check_operation_workflow_decision_outcome_group_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowDecisionOutcomeGroup")


@_attrs_define
class OperationWorkflowDecisionOutcomeGroup:
    """One exact contract, decision or path, rule version and reported outcome identity. Each contributing workflow counts
    once. Durations cover the full completed workflow and use the existing performance history rules; they do not
    measure rule causation or decision execution time. Overlapping blockers count once in each workflow's blocked
    duration.

    """

    kind: OperationWorkflowDecisionOutcomeGroupKind
    contract_version: int
    outcome_code: str
    completion_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    blocked_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    decision_code: str | Unset = UNSET
    rule_id: str | Unset = UNSET
    rule_version: str | Unset = UNSET
    group_id: str | Unset = UNSET
    path_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        contract_version = self.contract_version

        outcome_code = self.outcome_code

        completion_time = self.completion_time.to_dict()

        blocked_time = self.blocked_time.to_dict()

        decision_code = self.decision_code

        rule_id = self.rule_id

        rule_version = self.rule_version

        group_id = self.group_id

        path_id = self.path_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "contract_version": contract_version,
                "outcome_code": outcome_code,
                "completion_time": completion_time,
                "blocked_time": blocked_time,
            }
        )
        if decision_code is not UNSET:
            field_dict["decision_code"] = decision_code
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if rule_version is not UNSET:
            field_dict["rule_version"] = rule_version
        if group_id is not UNSET:
            field_dict["group_id"] = group_id
        if path_id is not UNSET:
            field_dict["path_id"] = path_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution

        d = dict(src_dict)
        kind = check_operation_workflow_decision_outcome_group_kind(d.pop("kind"))

        contract_version = d.pop("contract_version")

        outcome_code = d.pop("outcome_code")

        completion_time = OperationWorkflowDurationDistribution.from_dict(d.pop("completion_time"))

        blocked_time = OperationWorkflowDurationDistribution.from_dict(d.pop("blocked_time"))

        decision_code = d.pop("decision_code", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        rule_version = d.pop("rule_version", UNSET)

        group_id = d.pop("group_id", UNSET)

        path_id = d.pop("path_id", UNSET)

        operation_workflow_decision_outcome_group = cls(
            kind=kind,
            contract_version=contract_version,
            outcome_code=outcome_code,
            completion_time=completion_time,
            blocked_time=blocked_time,
            decision_code=decision_code,
            rule_id=rule_id,
            rule_version=rule_version,
            group_id=group_id,
            path_id=path_id,
        )

        return operation_workflow_decision_outcome_group
