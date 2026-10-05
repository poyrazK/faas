from typing import Literal

RouteMonitorReportStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_REPORT_STATUS_VALUES: set[RouteMonitorReportStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_report_status(value: str) -> RouteMonitorReportStatus:
    if value in ROUTE_MONITOR_REPORT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_REPORT_STATUS_VALUES!r}")
