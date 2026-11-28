from typing import Literal

RouteHealthClientErrorFindingStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_CLIENT_ERROR_FINDING_STATUS_VALUES: set[RouteHealthClientErrorFindingStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_client_error_finding_status(value: str) -> RouteHealthClientErrorFindingStatus:
    if value in ROUTE_HEALTH_CLIENT_ERROR_FINDING_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_CLIENT_ERROR_FINDING_STATUS_VALUES!r}")
