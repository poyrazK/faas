from typing import Literal

RouteMonitorCustomerCohortStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_CUSTOMER_COHORT_STATUS_VALUES: set[RouteMonitorCustomerCohortStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_customer_cohort_status(value: str) -> RouteMonitorCustomerCohortStatus:
    if value in ROUTE_MONITOR_CUSTOMER_COHORT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_COHORT_STATUS_VALUES!r}")
