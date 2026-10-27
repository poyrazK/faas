from typing import Literal

FinancialForecastResponseCurrency = Literal["USD"]

FINANCIAL_FORECAST_RESPONSE_CURRENCY_VALUES: set[FinancialForecastResponseCurrency] = {
    "USD",
}


def check_financial_forecast_response_currency(value: str) -> FinancialForecastResponseCurrency:
    if value in FINANCIAL_FORECAST_RESPONSE_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_FORECAST_RESPONSE_CURRENCY_VALUES!r}")
