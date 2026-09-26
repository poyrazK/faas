from typing import Literal

RequestAnalyticsComputeCostAllocationMethod = Literal["request_share"]

REQUEST_ANALYTICS_COMPUTE_COST_ALLOCATION_METHOD_VALUES: set[RequestAnalyticsComputeCostAllocationMethod] = {
    "request_share",
}


def check_request_analytics_compute_cost_allocation_method(value: str) -> RequestAnalyticsComputeCostAllocationMethod:
    if value in REQUEST_ANALYTICS_COMPUTE_COST_ALLOCATION_METHOD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_COMPUTE_COST_ALLOCATION_METHOD_VALUES!r}"
    )
