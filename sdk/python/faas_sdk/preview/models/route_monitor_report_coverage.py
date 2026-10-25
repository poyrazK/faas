from typing import Literal

RouteMonitorReportCoverage = Literal["observed_only"]

ROUTE_MONITOR_REPORT_COVERAGE_VALUES: set[RouteMonitorReportCoverage] = {
    "observed_only",
}


def check_route_monitor_report_coverage(value: str) -> RouteMonitorReportCoverage:
    if value in ROUTE_MONITOR_REPORT_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_REPORT_COVERAGE_VALUES!r}")
