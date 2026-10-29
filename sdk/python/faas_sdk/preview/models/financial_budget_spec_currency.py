from typing import Literal

FinancialBudgetSpecCurrency = Literal["EUR", "USD"]

FINANCIAL_BUDGET_SPEC_CURRENCY_VALUES: set[FinancialBudgetSpecCurrency] = {
    "EUR",
    "USD",
}


def check_financial_budget_spec_currency(value: str) -> FinancialBudgetSpecCurrency:
    if value in FINANCIAL_BUDGET_SPEC_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SPEC_CURRENCY_VALUES!r}")
