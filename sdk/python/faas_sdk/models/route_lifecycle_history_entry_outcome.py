from typing import Literal

RouteLifecycleHistoryEntryOutcome = Literal["applied", "blocked"]

ROUTE_LIFECYCLE_HISTORY_ENTRY_OUTCOME_VALUES: set[RouteLifecycleHistoryEntryOutcome] = {
    "applied",
    "blocked",
}


def check_route_lifecycle_history_entry_outcome(value: str) -> RouteLifecycleHistoryEntryOutcome:
    if value in ROUTE_LIFECYCLE_HISTORY_ENTRY_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_LIFECYCLE_HISTORY_ENTRY_OUTCOME_VALUES!r}")
