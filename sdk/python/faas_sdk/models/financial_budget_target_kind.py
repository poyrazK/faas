from typing import Literal

FinancialBudgetTargetKind = Literal["app", "job"]

FINANCIAL_BUDGET_TARGET_KIND_VALUES: set[FinancialBudgetTargetKind] = {
    "app",
    "job",
}


def check_financial_budget_target_kind(value: str) -> FinancialBudgetTargetKind:
    if value in FINANCIAL_BUDGET_TARGET_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_TARGET_KIND_VALUES!r}")
