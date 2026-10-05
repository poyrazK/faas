from typing import Literal

RouteHealthInvestigationStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_INVESTIGATION_STATUS_VALUES: set[RouteHealthInvestigationStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_investigation_status(value: str) -> RouteHealthInvestigationStatus:
    if value in ROUTE_HEALTH_INVESTIGATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_INVESTIGATION_STATUS_VALUES!r}")
