from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_budget_response import FinancialBudgetResponse


T = TypeVar("T", bound="FinancialBudgetListResponse")


@_attrs_define
class FinancialBudgetListResponse:
    """Nondeleted account-owned policy intents with separately reported readiness."""

    budgets: list[FinancialBudgetResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        budgets = []
        for budgets_item_data in self.budgets:
            budgets_item = budgets_item_data.to_dict()
            budgets.append(budgets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "budgets": budgets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_response import FinancialBudgetResponse

        d = dict(src_dict)
        budgets = []
        _budgets = d.pop("budgets")
        for budgets_item_data in _budgets:
            budgets_item = FinancialBudgetResponse.from_dict(budgets_item_data)

            budgets.append(budgets_item)

        financial_budget_list_response = cls(
            budgets=budgets,
        )

        financial_budget_list_response.additional_properties = d
        return financial_budget_list_response

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
