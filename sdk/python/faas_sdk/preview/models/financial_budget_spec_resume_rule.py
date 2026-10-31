from typing import Literal

FinancialBudgetSpecResumeRule = Literal["manual", "next_period"]

FINANCIAL_BUDGET_SPEC_RESUME_RULE_VALUES: set[FinancialBudgetSpecResumeRule] = {
    "manual",
    "next_period",
}


def check_financial_budget_spec_resume_rule(value: str) -> FinancialBudgetSpecResumeRule:
    if value in FINANCIAL_BUDGET_SPEC_RESUME_RULE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SPEC_RESUME_RULE_VALUES!r}")
