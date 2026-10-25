from typing import Literal

RouteHealthReportMode = Literal["enforce", "report"]

ROUTE_HEALTH_REPORT_MODE_VALUES: set[RouteHealthReportMode] = {
    "enforce",
    "report",
}


def check_route_health_report_mode(value: str) -> RouteHealthReportMode:
    if value in ROUTE_HEALTH_REPORT_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_REPORT_MODE_VALUES!r}")
