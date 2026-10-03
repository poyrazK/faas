from typing import Literal

RouteGateDecisionStatus = Literal["allowed", "blocked", "report_only"]

ROUTE_GATE_DECISION_STATUS_VALUES: set[RouteGateDecisionStatus] = {
    "allowed",
    "blocked",
    "report_only",
}


def check_route_gate_decision_status(value: str) -> RouteGateDecisionStatus:
    if value in ROUTE_GATE_DECISION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_GATE_DECISION_STATUS_VALUES!r}")
