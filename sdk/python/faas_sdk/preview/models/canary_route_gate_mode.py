from typing import Literal

CanaryRouteGateMode = Literal["enforce", "report"]

CANARY_ROUTE_GATE_MODE_VALUES: set[CanaryRouteGateMode] = {
    "enforce",
    "report",
}


def check_canary_route_gate_mode(value: str) -> CanaryRouteGateMode:
    if value in CANARY_ROUTE_GATE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CANARY_ROUTE_GATE_MODE_VALUES!r}")
