from typing import Literal

FinancialBudgetSpecMetersItem = Literal["compute", "egress", "object_storage"]

FINANCIAL_BUDGET_SPEC_METERS_ITEM_VALUES: set[FinancialBudgetSpecMetersItem] = {
    "compute",
    "egress",
    "object_storage",
}


def check_financial_budget_spec_meters_item(value: str) -> FinancialBudgetSpecMetersItem:
    if value in FINANCIAL_BUDGET_SPEC_METERS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SPEC_METERS_ITEM_VALUES!r}")
