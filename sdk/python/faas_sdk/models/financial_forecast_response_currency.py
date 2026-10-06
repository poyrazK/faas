from typing import Literal

FinancialForecastResponseCurrency = Literal["EUR"]

FINANCIAL_FORECAST_RESPONSE_CURRENCY_VALUES: set[FinancialForecastResponseCurrency] = {
    "EUR",
}


def check_financial_forecast_response_currency(value: str) -> FinancialForecastResponseCurrency:
    if value in FINANCIAL_FORECAST_RESPONSE_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_FORECAST_RESPONSE_CURRENCY_VALUES!r}")
