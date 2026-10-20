from typing import Literal

RouteHealthGateMode = Literal["enforce", "report"]

ROUTE_HEALTH_GATE_MODE_VALUES: set[RouteHealthGateMode] = {
    "enforce",
    "report",
}


def check_route_health_gate_mode(value: str) -> RouteHealthGateMode:
    if value in ROUTE_HEALTH_GATE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_GATE_MODE_VALUES!r}")
