from typing import Literal

RouteGateDecisionMode = Literal["enforce", "report"]

ROUTE_GATE_DECISION_MODE_VALUES: set[RouteGateDecisionMode] = {
    "enforce",
    "report",
}


def check_route_gate_decision_mode(value: str) -> RouteGateDecisionMode:
    if value in ROUTE_GATE_DECISION_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_GATE_DECISION_MODE_VALUES!r}")
