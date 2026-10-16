from typing import Literal

RouteAwakeResponseCoverage = Literal["retained_deployment_history"]

ROUTE_AWAKE_RESPONSE_COVERAGE_VALUES: set[RouteAwakeResponseCoverage] = {
    "retained_deployment_history",
}


def check_route_awake_response_coverage(value: str) -> RouteAwakeResponseCoverage:
    if value in ROUTE_AWAKE_RESPONSE_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_AWAKE_RESPONSE_COVERAGE_VALUES!r}")
