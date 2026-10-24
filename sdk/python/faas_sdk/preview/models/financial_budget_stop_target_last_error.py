from typing import Literal

FinancialBudgetStopTargetLastError = Literal[
    "instance_settlement_failed", "runtime_identity_unavailable", "scheduler_observation_stale", "vm_teardown_failed"
]

FINANCIAL_BUDGET_STOP_TARGET_LAST_ERROR_VALUES: set[FinancialBudgetStopTargetLastError] = {
    "instance_settlement_failed",
    "runtime_identity_unavailable",
    "scheduler_observation_stale",
    "vm_teardown_failed",
}


def check_financial_budget_stop_target_last_error(value: str) -> FinancialBudgetStopTargetLastError:
    if value in FINANCIAL_BUDGET_STOP_TARGET_LAST_ERROR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_STOP_TARGET_LAST_ERROR_VALUES!r}")
