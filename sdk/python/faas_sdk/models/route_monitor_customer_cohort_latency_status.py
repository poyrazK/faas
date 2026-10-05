from typing import Literal

RouteMonitorCustomerCohortLatencyStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_CUSTOMER_COHORT_LATENCY_STATUS_VALUES: set[RouteMonitorCustomerCohortLatencyStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_customer_cohort_latency_status(value: str) -> RouteMonitorCustomerCohortLatencyStatus:
    if value in ROUTE_MONITOR_CUSTOMER_COHORT_LATENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_COHORT_LATENCY_STATUS_VALUES!r}"
    )
