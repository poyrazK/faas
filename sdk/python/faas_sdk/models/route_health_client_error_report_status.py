from typing import Literal

RouteHealthClientErrorReportStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_CLIENT_ERROR_REPORT_STATUS_VALUES: set[RouteHealthClientErrorReportStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_client_error_report_status(value: str) -> RouteHealthClientErrorReportStatus:
    if value in ROUTE_HEALTH_CLIENT_ERROR_REPORT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_CLIENT_ERROR_REPORT_STATUS_VALUES!r}")
