from typing import Literal

RouteHealthReportClientErrorStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_REPORT_CLIENT_ERROR_STATUS_VALUES: set[RouteHealthReportClientErrorStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_report_client_error_status(value: str) -> RouteHealthReportClientErrorStatus:
    if value in ROUTE_HEALTH_REPORT_CLIENT_ERROR_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_REPORT_CLIENT_ERROR_STATUS_VALUES!r}")
