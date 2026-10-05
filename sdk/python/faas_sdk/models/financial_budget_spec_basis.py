from typing import Literal

FinancialBudgetSpecBasis = Literal["gross_usage", "net_usage"]

FINANCIAL_BUDGET_SPEC_BASIS_VALUES: set[FinancialBudgetSpecBasis] = {
    "gross_usage",
    "net_usage",
}


def check_financial_budget_spec_basis(value: str) -> FinancialBudgetSpecBasis:
    if value in FINANCIAL_BUDGET_SPEC_BASIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SPEC_BASIS_VALUES!r}")
