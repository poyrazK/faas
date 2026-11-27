from typing import Literal

ManagedPostgresTableHealthState = Literal["available", "parked", "unavailable"]

MANAGED_POSTGRES_TABLE_HEALTH_STATE_VALUES: set[ManagedPostgresTableHealthState] = {
    "available",
    "parked",
    "unavailable",
}


def check_managed_postgres_table_health_state(value: str) -> ManagedPostgresTableHealthState:
    if value in MANAGED_POSTGRES_TABLE_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_TABLE_HEALTH_STATE_VALUES!r}")
