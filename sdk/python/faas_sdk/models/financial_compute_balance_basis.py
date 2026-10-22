from typing import Literal

FinancialComputeBalanceBasis = Literal["gross_usage", "net_usage"]

FINANCIAL_COMPUTE_BALANCE_BASIS_VALUES: set[FinancialComputeBalanceBasis] = {
    "gross_usage",
    "net_usage",
}


def check_financial_compute_balance_basis(value: str) -> FinancialComputeBalanceBasis:
    if value in FINANCIAL_COMPUTE_BALANCE_BASIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_COMPUTE_BALANCE_BASIS_VALUES!r}")
