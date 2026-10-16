from typing import Literal

RouteAdviceResponseCacheabilityCoverage = Literal["deployment_isolated_observations_only"]

ROUTE_ADVICE_RESPONSE_CACHEABILITY_COVERAGE_VALUES: set[RouteAdviceResponseCacheabilityCoverage] = {
    "deployment_isolated_observations_only",
}


def check_route_advice_response_cacheability_coverage(value: str) -> RouteAdviceResponseCacheabilityCoverage:
    if value in ROUTE_ADVICE_RESPONSE_CACHEABILITY_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_RESPONSE_CACHEABILITY_COVERAGE_VALUES!r}"
    )
