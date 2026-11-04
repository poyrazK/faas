from typing import Literal

ManagedPostgresCutoverPreflightState = Literal["blocked", "pending"]

MANAGED_POSTGRES_CUTOVER_PREFLIGHT_STATE_VALUES: set[ManagedPostgresCutoverPreflightState] = {
    "blocked",
    "pending",
}


def check_managed_postgres_cutover_preflight_state(value: str) -> ManagedPostgresCutoverPreflightState:
    if value in MANAGED_POSTGRES_CUTOVER_PREFLIGHT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_PREFLIGHT_STATE_VALUES!r}")
