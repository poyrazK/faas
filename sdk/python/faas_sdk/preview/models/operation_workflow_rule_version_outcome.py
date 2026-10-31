from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowRuleVersionOutcome")


@_attrs_define
class OperationWorkflowRuleVersionOutcome:
    """One latest application-reported outcome among eligible workflows associated with this rule version. Up to 16
    outcomes are returned in code order. Basis points are rounded down, so displayed proportions may sum below 10000
    even without truncation.

    """

    outcome_code: str
    workflow_count: int
    proportion_basis_points: int

    def to_dict(self) -> dict[str, Any]:
        outcome_code = self.outcome_code

        workflow_count = self.workflow_count

        proportion_basis_points = self.proportion_basis_points

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome_code": outcome_code,
                "workflow_count": workflow_count,
                "proportion_basis_points": proportion_basis_points,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome_code = d.pop("outcome_code")

        workflow_count = d.pop("workflow_count")

        proportion_basis_points = d.pop("proportion_basis_points")

        operation_workflow_rule_version_outcome = cls(
            outcome_code=outcome_code,
            workflow_count=workflow_count,
            proportion_basis_points=proportion_basis_points,
        )

        return operation_workflow_rule_version_outcome
