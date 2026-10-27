from typing import Literal

RouteCustomerUsageResponseCoverage = Literal["observed_only"]

ROUTE_CUSTOMER_USAGE_RESPONSE_COVERAGE_VALUES: set[RouteCustomerUsageResponseCoverage] = {
    "observed_only",
}


def check_route_customer_usage_response_coverage(value: str) -> RouteCustomerUsageResponseCoverage:
    if value in ROUTE_CUSTOMER_USAGE_RESPONSE_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CUSTOMER_USAGE_RESPONSE_COVERAGE_VALUES!r}")
