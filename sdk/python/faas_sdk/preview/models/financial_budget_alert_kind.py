from typing import Literal

FinancialBudgetAlertKind = Literal["projection"]

FINANCIAL_BUDGET_ALERT_KIND_VALUES: set[FinancialBudgetAlertKind] = {
    "projection",
}


def check_financial_budget_alert_kind(value: str) -> FinancialBudgetAlertKind:
    if value in FINANCIAL_BUDGET_ALERT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_ALERT_KIND_VALUES!r}")
