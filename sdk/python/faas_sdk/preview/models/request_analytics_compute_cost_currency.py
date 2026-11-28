from typing import Literal

RequestAnalyticsComputeCostCurrency = Literal["EUR"]

REQUEST_ANALYTICS_COMPUTE_COST_CURRENCY_VALUES: set[RequestAnalyticsComputeCostCurrency] = {
    "EUR",
}


def check_request_analytics_compute_cost_currency(value: str) -> RequestAnalyticsComputeCostCurrency:
    if value in REQUEST_ANALYTICS_COMPUTE_COST_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_COMPUTE_COST_CURRENCY_VALUES!r}")
