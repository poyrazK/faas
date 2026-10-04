from typing import Literal

FinancialCostsResponseCurrency = Literal["EUR"]

FINANCIAL_COSTS_RESPONSE_CURRENCY_VALUES: set[FinancialCostsResponseCurrency] = {
    "EUR",
}


def check_financial_costs_response_currency(value: str) -> FinancialCostsResponseCurrency:
    if value in FINANCIAL_COSTS_RESPONSE_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_COSTS_RESPONSE_CURRENCY_VALUES!r}")
