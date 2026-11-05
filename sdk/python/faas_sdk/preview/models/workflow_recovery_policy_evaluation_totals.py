from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="WorkflowRecoveryPolicyEvaluationTotals")


@_attrs_define
class WorkflowRecoveryPolicyEvaluationTotals:
    """Counts of conditional preparation decisions and observed application pressure for the sampled candidates."""

    candidates: int
    would_prepare: int
    excluded: int
    unknown: int
    cooldown: int
    preparation_budget: int
    deduplicated: int
    execution_budget_pressure: int
    execution_cooldown_pressure: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        candidates = self.candidates

        would_prepare = self.would_prepare

        excluded = self.excluded

        unknown = self.unknown

        cooldown = self.cooldown

        preparation_budget = self.preparation_budget

        deduplicated = self.deduplicated

        execution_budget_pressure = self.execution_budget_pressure

        execution_cooldown_pressure = self.execution_cooldown_pressure

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "candidates": candidates,
                "would_prepare": would_prepare,
                "excluded": excluded,
                "unknown": unknown,
                "cooldown": cooldown,
                "preparation_budget": preparation_budget,
                "deduplicated": deduplicated,
                "execution_budget_pressure": execution_budget_pressure,
                "execution_cooldown_pressure": execution_cooldown_pressure,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        candidates = d.pop("candidates")

        would_prepare = d.pop("would_prepare")

        excluded = d.pop("excluded")

        unknown = d.pop("unknown")

        cooldown = d.pop("cooldown")

        preparation_budget = d.pop("preparation_budget")

        deduplicated = d.pop("deduplicated")

        execution_budget_pressure = d.pop("execution_budget_pressure")

        execution_cooldown_pressure = d.pop("execution_cooldown_pressure")

        workflow_recovery_policy_evaluation_totals = cls(
            candidates=candidates,
            would_prepare=would_prepare,
            excluded=excluded,
            unknown=unknown,
            cooldown=cooldown,
            preparation_budget=preparation_budget,
            deduplicated=deduplicated,
            execution_budget_pressure=execution_budget_pressure,
            execution_cooldown_pressure=execution_cooldown_pressure,
        )

        workflow_recovery_policy_evaluation_totals.additional_properties = d
        return workflow_recovery_policy_evaluation_totals

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
