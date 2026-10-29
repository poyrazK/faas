from typing import Literal

RouteHealthClientErrorWindowStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_CLIENT_ERROR_WINDOW_STATUS_VALUES: set[RouteHealthClientErrorWindowStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_client_error_window_status(value: str) -> RouteHealthClientErrorWindowStatus:
    if value in ROUTE_HEALTH_CLIENT_ERROR_WINDOW_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_CLIENT_ERROR_WINDOW_STATUS_VALUES!r}")
