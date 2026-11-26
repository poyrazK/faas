from typing import Literal

ManagedPostgresSessionsState = Literal["available", "parked", "unavailable"]

MANAGED_POSTGRES_SESSIONS_STATE_VALUES: set[ManagedPostgresSessionsState] = {
    "available",
    "parked",
    "unavailable",
}


def check_managed_postgres_sessions_state(value: str) -> ManagedPostgresSessionsState:
    if value in MANAGED_POSTGRES_SESSIONS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_SESSIONS_STATE_VALUES!r}")
