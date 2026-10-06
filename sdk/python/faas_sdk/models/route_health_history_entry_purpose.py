from typing import Literal

RouteHealthHistoryEntryPurpose = Literal["abort"]

ROUTE_HEALTH_HISTORY_ENTRY_PURPOSE_VALUES: set[RouteHealthHistoryEntryPurpose] = {
    "abort",
}


def check_route_health_history_entry_purpose(value: str) -> RouteHealthHistoryEntryPurpose:
    if value in ROUTE_HEALTH_HISTORY_ENTRY_PURPOSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_HISTORY_ENTRY_PURPOSE_VALUES!r}")
