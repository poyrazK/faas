from typing import Literal

FinancialBudgetStopTargetState = Literal["draining", "enforced", "failed", "requested", "unavailable"]

FINANCIAL_BUDGET_STOP_TARGET_STATE_VALUES: set[FinancialBudgetStopTargetState] = {
    "draining",
    "enforced",
    "failed",
    "requested",
    "unavailable",
}


def check_financial_budget_stop_target_state(value: str) -> FinancialBudgetStopTargetState:
    if value in FINANCIAL_BUDGET_STOP_TARGET_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_STOP_TARGET_STATE_VALUES!r}")
