from typing import Literal

FinancialBudgetAlertLastError = Literal["mail_delivery_failed"]

FINANCIAL_BUDGET_ALERT_LAST_ERROR_VALUES: set[FinancialBudgetAlertLastError] = {
    "mail_delivery_failed",
}


def check_financial_budget_alert_last_error(value: str) -> FinancialBudgetAlertLastError:
    if value in FINANCIAL_BUDGET_ALERT_LAST_ERROR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_ALERT_LAST_ERROR_VALUES!r}")
