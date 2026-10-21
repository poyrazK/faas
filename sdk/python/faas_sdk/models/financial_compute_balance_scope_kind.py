from typing import Literal

FinancialComputeBalanceScopeKind = Literal["account", "app", "job", "project"]

FINANCIAL_COMPUTE_BALANCE_SCOPE_KIND_VALUES: set[FinancialComputeBalanceScopeKind] = {
    "account",
    "app",
    "job",
    "project",
}


def check_financial_compute_balance_scope_kind(value: str) -> FinancialComputeBalanceScopeKind:
    if value in FINANCIAL_COMPUTE_BALANCE_SCOPE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_COMPUTE_BALANCE_SCOPE_KIND_VALUES!r}")
