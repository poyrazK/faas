from typing import Literal

FinancialAppCostsResponseCurrency = Literal["EUR"]

FINANCIAL_APP_COSTS_RESPONSE_CURRENCY_VALUES: set[FinancialAppCostsResponseCurrency] = {
    "EUR",
}


def check_financial_app_costs_response_currency(value: str) -> FinancialAppCostsResponseCurrency:
    if value in FINANCIAL_APP_COSTS_RESPONSE_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_APP_COSTS_RESPONSE_CURRENCY_VALUES!r}")
