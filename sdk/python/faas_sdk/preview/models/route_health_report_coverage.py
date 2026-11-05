from typing import Literal

RouteHealthReportCoverage = Literal["observed_only"]

ROUTE_HEALTH_REPORT_COVERAGE_VALUES: set[RouteHealthReportCoverage] = {
    "observed_only",
}


def check_route_health_report_coverage(value: str) -> RouteHealthReportCoverage:
    if value in ROUTE_HEALTH_REPORT_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_REPORT_COVERAGE_VALUES!r}")
