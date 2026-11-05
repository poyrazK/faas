from typing import Literal

RouteMonitorCustomerImpactCoverage = Literal["observed_only"]

ROUTE_MONITOR_CUSTOMER_IMPACT_COVERAGE_VALUES: set[RouteMonitorCustomerImpactCoverage] = {
    "observed_only",
}


def check_route_monitor_customer_impact_coverage(value: str) -> RouteMonitorCustomerImpactCoverage:
    if value in ROUTE_MONITOR_CUSTOMER_IMPACT_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_IMPACT_COVERAGE_VALUES!r}")
