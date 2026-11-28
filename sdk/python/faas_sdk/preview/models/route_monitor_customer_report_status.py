from typing import Literal

RouteMonitorCustomerReportStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_CUSTOMER_REPORT_STATUS_VALUES: set[RouteMonitorCustomerReportStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_customer_report_status(value: str) -> RouteMonitorCustomerReportStatus:
    if value in ROUTE_MONITOR_CUSTOMER_REPORT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_REPORT_STATUS_VALUES!r}")
