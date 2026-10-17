from typing import Literal

FinancialForecastCurrency = Literal["USD"]

FINANCIAL_FORECAST_CURRENCY_VALUES: set[FinancialForecastCurrency] = {
    "USD",
}


def check_financial_forecast_currency(value: str) -> FinancialForecastCurrency:
    if value in FINANCIAL_FORECAST_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_FORECAST_CURRENCY_VALUES!r}")
