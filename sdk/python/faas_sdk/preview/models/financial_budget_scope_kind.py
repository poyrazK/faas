from typing import Literal

FinancialBudgetScopeKind = Literal["account", "app", "environment", "job", "project"]

FINANCIAL_BUDGET_SCOPE_KIND_VALUES: set[FinancialBudgetScopeKind] = {
    "account",
    "app",
    "environment",
    "job",
    "project",
}


def check_financial_budget_scope_kind(value: str) -> FinancialBudgetScopeKind:
    if value in FINANCIAL_BUDGET_SCOPE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_SCOPE_KIND_VALUES!r}")
