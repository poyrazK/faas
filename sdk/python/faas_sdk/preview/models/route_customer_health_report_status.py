from typing import Literal

RouteCustomerHealthReportStatus = Literal["disabled", "healthy", "regressed", "unknown"]

ROUTE_CUSTOMER_HEALTH_REPORT_STATUS_VALUES: set[RouteCustomerHealthReportStatus] = {
    "disabled",
    "healthy",
    "regressed",
    "unknown",
}


def check_route_customer_health_report_status(value: str) -> RouteCustomerHealthReportStatus:
    if value in ROUTE_CUSTOMER_HEALTH_REPORT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CUSTOMER_HEALTH_REPORT_STATUS_VALUES!r}")
