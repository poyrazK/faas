from typing import Literal

SetRouteMonitorRequestOnViolation = Literal["report", "rollback"]

SET_ROUTE_MONITOR_REQUEST_ON_VIOLATION_VALUES: set[SetRouteMonitorRequestOnViolation] = {
    "report",
    "rollback",
}


def check_set_route_monitor_request_on_violation(value: str) -> SetRouteMonitorRequestOnViolation:
    if value in SET_ROUTE_MONITOR_REQUEST_ON_VIOLATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_ROUTE_MONITOR_REQUEST_ON_VIOLATION_VALUES!r}")
