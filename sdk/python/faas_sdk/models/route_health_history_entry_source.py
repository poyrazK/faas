from typing import Literal

RouteHealthHistoryEntrySource = Literal["manual", "worker"]

ROUTE_HEALTH_HISTORY_ENTRY_SOURCE_VALUES: set[RouteHealthHistoryEntrySource] = {
    "manual",
    "worker",
}


def check_route_health_history_entry_source(value: str) -> RouteHealthHistoryEntrySource:
    if value in ROUTE_HEALTH_HISTORY_ENTRY_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_HISTORY_ENTRY_SOURCE_VALUES!r}")
