from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.financial_budget_alert import FinancialBudgetAlert
    from ..models.financial_budget_evaluation import FinancialBudgetEvaluation


T = TypeVar("T", bound="FinancialBudgetRuntime")


@_attrs_define
class FinancialBudgetRuntime:
    """Current policy revision and UTC month only; a missing evaluation means monitoring has not evaluated that revision in
    this period.

    """

    alerts: list[FinancialBudgetAlert]
    evaluation: FinancialBudgetEvaluation | Unset = UNSET
    """Retained evidence boundary; an absent known_millicents means evaluation unavailable, never zero. Partial
    known costs can trigger labelled notifications; stale observations cannot create new alerts."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        alerts = []
        for alerts_item_data in self.alerts:
            alerts_item = alerts_item_data.to_dict()
            alerts.append(alerts_item)

        evaluation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evaluation, Unset):
            evaluation = self.evaluation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "alerts": alerts,
            }
        )
        if evaluation is not UNSET:
            field_dict["evaluation"] = evaluation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_alert import FinancialBudgetAlert
        from ..models.financial_budget_evaluation import FinancialBudgetEvaluation

        d = dict(src_dict)
        alerts = []
        _alerts = d.pop("alerts")
        for alerts_item_data in _alerts:
            alerts_item = FinancialBudgetAlert.from_dict(alerts_item_data)

            alerts.append(alerts_item)

        _evaluation = d.pop("evaluation", UNSET)
        evaluation: FinancialBudgetEvaluation | Unset
        if isinstance(_evaluation, Unset):
            evaluation = UNSET
        else:
            evaluation = FinancialBudgetEvaluation.from_dict(_evaluation)

        financial_budget_runtime = cls(
            alerts=alerts,
            evaluation=evaluation,
        )

        financial_budget_runtime.additional_properties = d
        return financial_budget_runtime

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
