from typing import Literal

RouteHealthInvestigationCoverage = Literal["observed_only"]

ROUTE_HEALTH_INVESTIGATION_COVERAGE_VALUES: set[RouteHealthInvestigationCoverage] = {
    "observed_only",
}


def check_route_health_investigation_coverage(value: str) -> RouteHealthInvestigationCoverage:
    if value in ROUTE_HEALTH_INVESTIGATION_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_INVESTIGATION_COVERAGE_VALUES!r}")
