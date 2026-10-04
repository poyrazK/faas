from typing import Literal

RouteHealthFindingStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_FINDING_STATUS_VALUES: set[RouteHealthFindingStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_finding_status(value: str) -> RouteHealthFindingStatus:
    if value in ROUTE_HEALTH_FINDING_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_FINDING_STATUS_VALUES!r}")
