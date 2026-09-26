from typing import Literal

RequestAnalyticsComputeCostBasis = Literal["raw_ram_hours_at_current_overage_rate_before_allowance"]

REQUEST_ANALYTICS_COMPUTE_COST_BASIS_VALUES: set[RequestAnalyticsComputeCostBasis] = {
    "raw_ram_hours_at_current_overage_rate_before_allowance",
}


def check_request_analytics_compute_cost_basis(value: str) -> RequestAnalyticsComputeCostBasis:
    if value in REQUEST_ANALYTICS_COMPUTE_COST_BASIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_COMPUTE_COST_BASIS_VALUES!r}")
