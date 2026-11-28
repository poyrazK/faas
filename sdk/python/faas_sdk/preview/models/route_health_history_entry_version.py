from typing import Literal

RouteHealthHistoryEntryVersion = Literal[1]

ROUTE_HEALTH_HISTORY_ENTRY_VERSION_VALUES: set[RouteHealthHistoryEntryVersion] = {
    1,
}


def check_route_health_history_entry_version(value: int) -> RouteHealthHistoryEntryVersion:
    if value in ROUTE_HEALTH_HISTORY_ENTRY_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_HISTORY_ENTRY_VERSION_VALUES!r}")
