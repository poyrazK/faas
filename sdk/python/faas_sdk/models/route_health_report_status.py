from typing import Literal

RouteHealthReportStatus = Literal["disabled", "healthy", "regressed", "unknown"]

ROUTE_HEALTH_REPORT_STATUS_VALUES: set[RouteHealthReportStatus] = {
    "disabled",
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_report_status(value: str) -> RouteHealthReportStatus:
    if value in ROUTE_HEALTH_REPORT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_REPORT_STATUS_VALUES!r}")
