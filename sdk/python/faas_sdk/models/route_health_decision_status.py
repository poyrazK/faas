from typing import Literal

RouteHealthDecisionStatus = Literal["aborted", "allowed", "blocked", "report_only"]

ROUTE_HEALTH_DECISION_STATUS_VALUES: set[RouteHealthDecisionStatus] = {
    "aborted",
    "allowed",
    "blocked",
    "report_only",
}


def check_route_health_decision_status(value: str) -> RouteHealthDecisionStatus:
    if value in ROUTE_HEALTH_DECISION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_DECISION_STATUS_VALUES!r}")
