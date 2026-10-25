from typing import Literal

FinancialPriceContractPlan = Literal["free", "hobby", "pro", "scale"]

FINANCIAL_PRICE_CONTRACT_PLAN_VALUES: set[FinancialPriceContractPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_financial_price_contract_plan(value: str) -> FinancialPriceContractPlan:
    if value in FINANCIAL_PRICE_CONTRACT_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_PRICE_CONTRACT_PLAN_VALUES!r}")
