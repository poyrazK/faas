from typing import Literal

AppHealthHistoryEntryKind = Literal["baseline", "gap", "transition"]

APP_HEALTH_HISTORY_ENTRY_KIND_VALUES: set[AppHealthHistoryEntryKind] = {
    "baseline",
    "gap",
    "transition",
}


def check_app_health_history_entry_kind(value: str) -> AppHealthHistoryEntryKind:
    if value in APP_HEALTH_HISTORY_ENTRY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_HISTORY_ENTRY_KIND_VALUES!r}")
