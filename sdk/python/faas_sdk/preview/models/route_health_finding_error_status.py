from typing import Literal

RouteHealthFindingErrorStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_FINDING_ERROR_STATUS_VALUES: set[RouteHealthFindingErrorStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_finding_error_status(value: str) -> RouteHealthFindingErrorStatus:
    if value in ROUTE_HEALTH_FINDING_ERROR_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_FINDING_ERROR_STATUS_VALUES!r}")
