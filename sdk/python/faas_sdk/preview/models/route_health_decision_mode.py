from typing import Literal

RouteHealthDecisionMode = Literal["enforce", "report"]

ROUTE_HEALTH_DECISION_MODE_VALUES: set[RouteHealthDecisionMode] = {
    "enforce",
    "report",
}


def check_route_health_decision_mode(value: str) -> RouteHealthDecisionMode:
    if value in ROUTE_HEALTH_DECISION_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_DECISION_MODE_VALUES!r}")
