from typing import Literal

RouteHealthLatencyDiagnosticsCoverage = Literal["retained_samples"]

ROUTE_HEALTH_LATENCY_DIAGNOSTICS_COVERAGE_VALUES: set[RouteHealthLatencyDiagnosticsCoverage] = {
    "retained_samples",
}


def check_route_health_latency_diagnostics_coverage(value: str) -> RouteHealthLatencyDiagnosticsCoverage:
    if value in ROUTE_HEALTH_LATENCY_DIAGNOSTICS_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_LATENCY_DIAGNOSTICS_COVERAGE_VALUES!r}")
