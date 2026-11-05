from typing import Literal

RouteMonitorConfigOnViolation = Literal["rollback"]

ROUTE_MONITOR_CONFIG_ON_VIOLATION_VALUES: set[RouteMonitorConfigOnViolation] = {
    "rollback",
}


def check_route_monitor_config_on_violation(value: str) -> RouteMonitorConfigOnViolation:
    if value in ROUTE_MONITOR_CONFIG_ON_VIOLATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CONFIG_ON_VIOLATION_VALUES!r}")
