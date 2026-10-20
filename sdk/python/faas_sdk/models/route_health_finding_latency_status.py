from typing import Literal

RouteHealthFindingLatencyStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_FINDING_LATENCY_STATUS_VALUES: set[RouteHealthFindingLatencyStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_finding_latency_status(value: str) -> RouteHealthFindingLatencyStatus:
    if value in ROUTE_HEALTH_FINDING_LATENCY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_FINDING_LATENCY_STATUS_VALUES!r}")
