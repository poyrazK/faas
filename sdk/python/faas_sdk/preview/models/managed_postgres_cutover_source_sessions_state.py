from typing import Literal

ManagedPostgresCutoverSourceSessionsState = Literal[
    "abandoning", "closed", "closing", "not_requested", "pending", "released", "unavailable"
]

MANAGED_POSTGRES_CUTOVER_SOURCE_SESSIONS_STATE_VALUES: set[ManagedPostgresCutoverSourceSessionsState] = {
    "abandoning",
    "closed",
    "closing",
    "not_requested",
    "pending",
    "released",
    "unavailable",
}


def check_managed_postgres_cutover_source_sessions_state(value: str) -> ManagedPostgresCutoverSourceSessionsState:
    if value in MANAGED_POSTGRES_CUTOVER_SOURCE_SESSIONS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_SOURCE_SESSIONS_STATE_VALUES!r}"
    )
