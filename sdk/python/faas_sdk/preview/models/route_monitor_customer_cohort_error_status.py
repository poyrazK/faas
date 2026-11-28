from typing import Literal

RouteMonitorCustomerCohortErrorStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_CUSTOMER_COHORT_ERROR_STATUS_VALUES: set[RouteMonitorCustomerCohortErrorStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_customer_cohort_error_status(value: str) -> RouteMonitorCustomerCohortErrorStatus:
    if value in ROUTE_MONITOR_CUSTOMER_COHORT_ERROR_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_COHORT_ERROR_STATUS_VALUES!r}"
    )
