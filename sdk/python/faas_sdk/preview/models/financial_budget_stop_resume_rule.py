from typing import Literal

FinancialBudgetStopResumeRule = Literal["manual", "next_period"]

FINANCIAL_BUDGET_STOP_RESUME_RULE_VALUES: set[FinancialBudgetStopResumeRule] = {
    "manual",
    "next_period",
}


def check_financial_budget_stop_resume_rule(value: str) -> FinancialBudgetStopResumeRule:
    if value in FINANCIAL_BUDGET_STOP_RESUME_RULE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_STOP_RESUME_RULE_VALUES!r}")
