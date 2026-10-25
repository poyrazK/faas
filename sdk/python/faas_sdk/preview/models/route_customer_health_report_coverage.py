from typing import Literal

RouteCustomerHealthReportCoverage = Literal["observed_only"]

ROUTE_CUSTOMER_HEALTH_REPORT_COVERAGE_VALUES: set[RouteCustomerHealthReportCoverage] = {
    "observed_only",
}


def check_route_customer_health_report_coverage(value: str) -> RouteCustomerHealthReportCoverage:
    if value in ROUTE_CUSTOMER_HEALTH_REPORT_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CUSTOMER_HEALTH_REPORT_COVERAGE_VALUES!r}")
