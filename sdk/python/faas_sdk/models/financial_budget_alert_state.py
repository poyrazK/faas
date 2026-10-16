from typing import Literal

FinancialBudgetAlertState = Literal["delivered", "pending", "retrying", "sending"]

FINANCIAL_BUDGET_ALERT_STATE_VALUES: set[FinancialBudgetAlertState] = {
    "delivered",
    "pending",
    "retrying",
    "sending",
}


def check_financial_budget_alert_state(value: str) -> FinancialBudgetAlertState:
    if value in FINANCIAL_BUDGET_ALERT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_ALERT_STATE_VALUES!r}")
