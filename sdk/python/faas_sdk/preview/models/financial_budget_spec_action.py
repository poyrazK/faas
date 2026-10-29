from typing import Literal

FinancialBudgetSpecAction = Literal[
    "notify", "reject_traffic", "stop_previews", "suspend_background", "suspend_workloads"
]

FINANCIAL_BUDGET_SPEC_ACTION_VALUES: set[FinancialBudgetSpecAction] = {
    "notify",
    "reject_traffic",
    "stop_previews",
    "suspend_background",
    "suspend_workloads",
}


def check_financial_budget_spec_action(value: str) -> FinancialBudgetSpecAction:
    if value in FINANCIAL_BUDGET_SPEC_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SPEC_ACTION_VALUES!r}")
