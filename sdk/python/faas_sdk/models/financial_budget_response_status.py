from typing import Literal

FinancialBudgetResponseStatus = Literal["deleted", "draft", "unavailable"]

FINANCIAL_BUDGET_RESPONSE_STATUS_VALUES: set[FinancialBudgetResponseStatus] = {
    "deleted",
    "draft",
    "unavailable",
}


def check_financial_budget_response_status(value: str) -> FinancialBudgetResponseStatus:
    if value in FINANCIAL_BUDGET_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_RESPONSE_STATUS_VALUES!r}")
