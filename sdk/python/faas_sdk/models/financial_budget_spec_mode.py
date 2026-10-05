from typing import Literal

FinancialBudgetSpecMode = Literal["monitored", "strict"]

FINANCIAL_BUDGET_SPEC_MODE_VALUES: set[FinancialBudgetSpecMode] = {
    "monitored",
    "strict",
}


def check_financial_budget_spec_mode(value: str) -> FinancialBudgetSpecMode:
    if value in FINANCIAL_BUDGET_SPEC_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SPEC_MODE_VALUES!r}")
