from typing import Literal

SetRouteHealthGateRequestMode = Literal["enforce", "report"]

SET_ROUTE_HEALTH_GATE_REQUEST_MODE_VALUES: set[SetRouteHealthGateRequestMode] = {
    "enforce",
    "report",
}


def check_set_route_health_gate_request_mode(value: str) -> SetRouteHealthGateRequestMode:
    if value in SET_ROUTE_HEALTH_GATE_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_ROUTE_HEALTH_GATE_REQUEST_MODE_VALUES!r}")
