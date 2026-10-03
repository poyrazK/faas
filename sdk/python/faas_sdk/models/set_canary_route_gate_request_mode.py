from typing import Literal

SetCanaryRouteGateRequestMode = Literal["enforce", "report"]

SET_CANARY_ROUTE_GATE_REQUEST_MODE_VALUES: set[SetCanaryRouteGateRequestMode] = {
    "enforce",
    "report",
}


def check_set_canary_route_gate_request_mode(value: str) -> SetCanaryRouteGateRequestMode:
    if value in SET_CANARY_ROUTE_GATE_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_CANARY_ROUTE_GATE_REQUEST_MODE_VALUES!r}")
