from typing import Literal

ManagedPostgresBaseRefreshOutcome = Literal[
    "interrupted", "running", "runtime_failed", "still_stale", "succeeded", "window_closed"
]

MANAGED_POSTGRES_BASE_REFRESH_OUTCOME_VALUES: set[ManagedPostgresBaseRefreshOutcome] = {
    "interrupted",
    "running",
    "runtime_failed",
    "still_stale",
    "succeeded",
    "window_closed",
}


def check_managed_postgres_base_refresh_outcome(value: str) -> ManagedPostgresBaseRefreshOutcome:
    if value in MANAGED_POSTGRES_BASE_REFRESH_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_BASE_REFRESH_OUTCOME_VALUES!r}")
