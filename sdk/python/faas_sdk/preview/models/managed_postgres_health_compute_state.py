from typing import Literal

ManagedPostgresHealthComputeState = Literal["active", "suspended", "unknown", "waking"]

MANAGED_POSTGRES_HEALTH_COMPUTE_STATE_VALUES: set[ManagedPostgresHealthComputeState] = {
    "active",
    "suspended",
    "unknown",
    "waking",
}


def check_managed_postgres_health_compute_state(value: str) -> ManagedPostgresHealthComputeState:
    if value in MANAGED_POSTGRES_HEALTH_COMPUTE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_HEALTH_COMPUTE_STATE_VALUES!r}")
