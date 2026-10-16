from typing import Literal

RouteAdviceResponseCoverage = Literal["retained_deployment_history"]

ROUTE_ADVICE_RESPONSE_COVERAGE_VALUES: set[RouteAdviceResponseCoverage] = {
    "retained_deployment_history",
}


def check_route_advice_response_coverage(value: str) -> RouteAdviceResponseCoverage:
    if value in ROUTE_ADVICE_RESPONSE_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_RESPONSE_COVERAGE_VALUES!r}")
