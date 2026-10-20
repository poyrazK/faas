from typing import Literal

FinancialBudgetSpecMetersItem = Literal["compute", "egress"]

FINANCIAL_BUDGET_SPEC_METERS_ITEM_VALUES: set[FinancialBudgetSpecMetersItem] = {
    "compute",
    "egress",
}


def check_financial_budget_spec_meters_item(value: str) -> FinancialBudgetSpecMetersItem:
    if value in FINANCIAL_BUDGET_SPEC_METERS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SPEC_METERS_ITEM_VALUES!r}")
