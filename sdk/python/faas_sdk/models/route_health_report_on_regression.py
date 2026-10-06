from typing import Literal

RouteHealthReportOnRegression = Literal["abort", "hold"]

ROUTE_HEALTH_REPORT_ON_REGRESSION_VALUES: set[RouteHealthReportOnRegression] = {
    "abort",
    "hold",
}


def check_route_health_report_on_regression(value: str) -> RouteHealthReportOnRegression:
    if value in ROUTE_HEALTH_REPORT_ON_REGRESSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_REPORT_ON_REGRESSION_VALUES!r}")
