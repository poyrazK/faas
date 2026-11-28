from typing import Literal

RouteMonitorCustomerReportCoverage = Literal["observed_only"]

ROUTE_MONITOR_CUSTOMER_REPORT_COVERAGE_VALUES: set[RouteMonitorCustomerReportCoverage] = {
    "observed_only",
}


def check_route_monitor_customer_report_coverage(value: str) -> RouteMonitorCustomerReportCoverage:
    if value in ROUTE_MONITOR_CUSTOMER_REPORT_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_REPORT_COVERAGE_VALUES!r}")
